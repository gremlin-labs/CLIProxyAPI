package management

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/modelintel"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/util"
	log "github.com/sirupsen/logrus"
)

// modelIntelligenceSource is the attribution Artificial Analysis requires.
type modelIntelligenceSource struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// modelIntelligenceResponse is the GET contract. It never carries the API key.
type modelIntelligenceResponse struct {
	Configured               bool                    `json:"configured"`
	KeySource                string                  `json:"key_source"`
	Tier                     string                  `json:"tier"`
	ConfiguredTier           string                  `json:"configured_tier"`
	CacheTTL                 string                  `json:"cache_ttl"`
	IntelligenceIndexVersion json.RawMessage         `json:"intelligence_index_version"`
	FetchedAt                *string                 `json:"fetched_at"`
	Cached                   bool                    `json:"cached"`
	Warning                  string                  `json:"warning,omitempty"`
	RetryAfter               *int                    `json:"retry_after,omitempty"`
	Source                   modelIntelligenceSource `json:"source"`
	Models                   []modelintel.Model      `json:"models"`
}

// GetModelIntelligence serves Artificial Analysis benchmark data from the cache,
// fetching upstream when stale or when refresh=1. Without a key it reports
// configured=false and makes no upstream call.
func (h *Handler) GetModelIntelligence(c *gin.Context) {
	h.mu.Lock()
	if h.cfg == nil {
		h.mu.Unlock()
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "config unavailable"})
		return
	}
	settings := modelintel.ResolveSettings(h.cfg.ModelIntelligence)
	client := util.SetProxy(&h.cfg.SDKConfig, &http.Client{})
	service := h.modelIntel
	h.mu.Unlock()

	resp := modelIntelligenceResponse{
		Configured:     settings.Configured(),
		KeySource:      settings.KeySource,
		Tier:           settings.Tier,
		ConfiguredTier: settings.Tier,
		CacheTTL:       settings.CacheTTL.String(),
		Source:         modelIntelligenceSource{Name: modelintel.SourceName, URL: modelintel.SourceURL},
		Models:         []modelintel.Model{},
	}
	if !settings.Configured() {
		c.JSON(http.StatusOK, resp)
		return
	}

	refresh := isTruthyQuery(c.Query("refresh"))
	result, err := service.Get(c.Request.Context(), client, settings, refresh)
	if err != nil {
		writeModelIntelligenceError(c, err)
		return
	}
	snap := result.Snapshot
	if snap.Tier != "" {
		resp.Tier = snap.Tier
	}
	resp.IntelligenceIndexVersion = snap.IntelligenceIndexVersion
	fetchedAt := snap.FetchedAt.UTC().Format(time.RFC3339)
	resp.FetchedAt = &fetchedAt
	resp.Cached = result.Cached
	resp.Warning = result.Warning
	if result.Warning != "" {
		retryAfter := result.RetryAfter
		resp.RetryAfter = &retryAfter
	}
	if snap.Models != nil {
		resp.Models = snap.Models
	}
	c.JSON(http.StatusOK, resp)
}

func writeModelIntelligenceError(c *gin.Context, err error) {
	if c.Request.Context().Err() != nil {
		// The client went away; nothing useful can be written.
		c.AbortWithStatus(499)
		return
	}
	upstream, ok := modelintel.IsUpstreamError(err)
	if !ok {
		log.WithError(err).Warn("model intelligence: upstream request failed")
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error(), "upstream_status": 0})
		return
	}
	log.WithField("upstream_status", upstream.Status).Warn("model intelligence: " + upstream.Message)
	if upstream.RateLimited() {
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error":           upstream.Message,
			"upstream_status": upstream.Status,
			"retry_after":     upstream.RetryAfter,
		})
		return
	}
	c.JSON(http.StatusBadGateway, gin.H{"error": upstream.Message, "upstream_status": upstream.Status})
}

func isTruthyQuery(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes":
		return true
	}
	return false
}

// PutModelIntelligenceConfig stores the Artificial Analysis settings in the
// config file and clears the cache. An omitted field is left unchanged; an
// empty api_key removes the stored key. The key is never echoed back.
func (h *Handler) PutModelIntelligenceConfig(c *gin.Context) {
	var body struct {
		APIKey   *string `json:"api_key"`
		Tier     *string `json:"tier"`
		CacheTTL *string `json:"cache_ttl"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	if body.Tier != nil && strings.TrimSpace(*body.Tier) != "" && !modelintel.ValidTier(*body.Tier) {
		c.JSON(http.StatusBadRequest, gin.H{"error": `tier must be "free" or "pro"`})
		return
	}
	if body.CacheTTL != nil && strings.TrimSpace(*body.CacheTTL) != "" {
		ttl, err := time.ParseDuration(strings.TrimSpace(*body.CacheTTL))
		if err != nil || ttl <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "cache_ttl must be a positive duration such as 6h"})
			return
		}
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cfg == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "config unavailable"})
		return
	}
	mi := &h.cfg.ModelIntelligence
	if body.APIKey != nil {
		mi.ArtificialAnalysisAPIKey = strings.TrimSpace(*body.APIKey)
	}
	if body.Tier != nil {
		mi.ArtificialAnalysisTier = strings.ToLower(strings.TrimSpace(*body.Tier))
	}
	if body.CacheTTL != nil {
		mi.CacheTTL = strings.TrimSpace(*body.CacheTTL)
	}
	snapshot, ok := h.saveConfigAndSnapshotLocked(c)
	if !ok {
		return
	}
	h.modelIntel.Clear()
	settings := modelintel.ResolveSettings(*mi)
	c.JSON(http.StatusOK, gin.H{
		"status":          "ok",
		"configured":      settings.Configured(),
		"key_source":      settings.KeySource,
		"configured_tier": settings.Tier,
		"cache_ttl":       settings.CacheTTL.String(),
	})
	h.reloadConfigAfterManagementSaveAsync(c.Request.Context(), snapshot)
}
