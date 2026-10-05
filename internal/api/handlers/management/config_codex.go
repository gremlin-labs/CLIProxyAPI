package management

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
)

// Codex Config page: private instructions, routing preferences and failure policy.
// The routes return and accept the provider-wide `codex` settings as flat JSON.

// GetCodexInstructions returns the private Codex instruction settings.
func (h *Handler) GetCodexInstructions(c *gin.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c.JSON(http.StatusOK, h.cfg.Codex.Instructions)
}

// PutCodexInstructions replaces the private Codex instruction settings.
func (h *Handler) PutCodexInstructions(c *gin.Context) {
	var body config.CodexInstructionsConfig
	if errBind := c.ShouldBindJSON(&body); errBind != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body", "message": errBind.Error()})
		return
	}
	normalized, ok := config.NormalizeCodexInstructionsConfig(body)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid mode", "message": "mode must be prepend, append, or replace"})
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cfg.Codex.Instructions = normalized
	h.persistLocked(c)
}

// GetCodexRoutingConfig returns the Codex routing preferences.
func (h *Handler) GetCodexRoutingConfig(c *gin.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c.JSON(http.StatusOK, h.cfg.Codex.Routing)
}

// PutCodexRoutingConfig replaces the Codex routing preferences.
func (h *Handler) PutCodexRoutingConfig(c *gin.Context) {
	var body config.CodexRoutingConfig
	if errBind := c.ShouldBindJSON(&body); errBind != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body", "message": errBind.Error()})
		return
	}
	strategy, ok := config.NormalizeCodexRoutingStrategy(body.Strategy)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid strategy", "message": "strategy must be empty or adaptive"})
		return
	}
	body.Strategy = strategy
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cfg.Codex.Routing = body
	h.persistLocked(c)
}

type codexFailureConfigBody struct {
	AutoDisableAuthFailures         *bool `json:"auto-disable-auth-failures"`
	AuthFailureDisableAfter         *int  `json:"auth-failure-disable-after"`
	UsageLimitDisableAfter          *int  `json:"usage-limit-disable-after"`
	UsageLimitCooldownFallbackHours *int  `json:"usage-limit-cooldown-fallback-hours"`
}

// GetCodexFailureConfig returns the effective Codex credential failure policy.
func (h *Handler) GetCodexFailureConfig(c *gin.Context) {
	h.mu.Lock()
	policy := h.cfg.Codex.FailurePolicy()
	h.mu.Unlock()
	c.JSON(http.StatusOK, gin.H{
		"auto-disable-auth-failures":          policy.AutoDisableAuthFailures,
		"auth-failure-disable-after":          policy.AuthFailureDisableAfter,
		"usage-limit-disable-after":           policy.UsageLimitDisableAfter,
		"usage-limit-cooldown-fallback-hours": policy.UsageLimitCooldownFallbackHours,
	})
}

// PutCodexFailureConfig updates the Codex credential failure policy. Keys omitted
// from the body keep their current values; negative counts are stored as 0.
func (h *Handler) PutCodexFailureConfig(c *gin.Context) {
	var body codexFailureConfigBody
	if errBind := c.ShouldBindJSON(&body); errBind != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body", "message": errBind.Error()})
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if body.AutoDisableAuthFailures != nil {
		value := *body.AutoDisableAuthFailures
		h.cfg.Codex.AutoDisableAuthFailures = &value
	}
	if body.AuthFailureDisableAfter != nil {
		h.cfg.Codex.AuthFailureDisableAfter = nonNegativeIntPtr(*body.AuthFailureDisableAfter)
	}
	if body.UsageLimitDisableAfter != nil {
		h.cfg.Codex.UsageLimitDisableAfter = nonNegativeIntPtr(*body.UsageLimitDisableAfter)
	}
	if body.UsageLimitCooldownFallbackHours != nil {
		h.cfg.Codex.UsageLimitCooldownFallbackHours = nonNegativeIntPtr(*body.UsageLimitCooldownFallbackHours)
	}
	h.persistLocked(c)
}

func nonNegativeIntPtr(value int) *int {
	value = max(value, 0)
	return &value
}
