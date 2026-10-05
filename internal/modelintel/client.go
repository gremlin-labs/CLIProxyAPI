// Package modelintel fetches model benchmark data from the Artificial Analysis
// Data API and caches it for the management API. The API key stays server-side.
package modelintel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

const (
	// DefaultBaseURL is the Artificial Analysis Data API v2 root.
	DefaultBaseURL = "https://artificialanalysis.ai/api/v2"
	// UserAgent identifies this proxy honestly to Artificial Analysis.
	UserAgent = "CLIProxyAPI-GLE-model-intelligence/1.0 (+https://github.com/gremlin-labs/CLIProxyAPI-GLE)"
	// SourceName and SourceURL are the attribution Artificial Analysis requires.
	SourceName = "Artificial Analysis"
	SourceURL  = "https://artificialanalysis.ai"

	// TierFree uses /language/models/free; TierPro uses /language/models.
	TierFree = "free"
	TierPro  = "pro"

	// maxPages bounds pagination so a misbehaving upstream cannot loop forever.
	maxPages = 50
	// maxBodyBytes bounds a single page response.
	maxBodyBytes = 32 << 20
)

// Model is the normalized, panel-facing view of one Artificial Analysis model.
// Nil pointers mean the value was not measured upstream.
type Model struct {
	ID                    string   `json:"id"`
	Name                  string   `json:"name"`
	Slug                  string   `json:"slug"`
	Creator               Creator  `json:"creator"`
	ReleaseDate           *string  `json:"release_date"`
	Reasoning             bool     `json:"reasoning"`
	OpenWeights           *bool    `json:"open_weights"`
	ContextWindow         *float64 `json:"context_window"`
	IntelligenceIndex     *float64 `json:"intelligence_index"`
	CodingIndex           *float64 `json:"coding_index"`
	AgenticIndex          *float64 `json:"agentic_index"`
	PriceInput            *float64 `json:"price_input"`
	PriceOutput           *float64 `json:"price_output"`
	PriceBlended          *float64 `json:"price_blended"`
	IndexCost             *float64 `json:"index_cost"`
	OutputTokensPerSecond *float64 `json:"output_tokens_per_second"`
	TimeToFirstToken      *float64 `json:"time_to_first_token"`
	EndToEndSeconds       *float64 `json:"end_to_end_seconds"`
	OpenRouterID          *string  `json:"openrouter_id"`
}

// Creator is the model's publisher.
type Creator struct {
	Name    string  `json:"name"`
	Country *string `json:"country"`
}

// Snapshot is one complete fetch of every page for a tier.
type Snapshot struct {
	// Tier is the tier reported by Artificial Analysis.
	Tier string
	// IntelligenceIndexVersion is passed through verbatim (string or number); nil when absent.
	IntelligenceIndexVersion json.RawMessage
	FetchedAt                time.Time
	Models                   []Model
}

// UpstreamError describes a non-2xx Artificial Analysis response.
type UpstreamError struct {
	Status int
	// RetryAfter is the Retry-After delay in seconds for 429 responses (0 when unknown).
	RetryAfter int
	Message    string
}

func (e *UpstreamError) Error() string { return e.Message }

// Unauthorized reports whether the key was rejected.
func (e *UpstreamError) Unauthorized() bool {
	return e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden
}

// RateLimited reports whether the daily quota is exhausted.
func (e *UpstreamError) RateLimited() bool { return e.Status == http.StatusTooManyRequests }

type apiResponse struct {
	Tier                     string          `json:"tier"`
	IntelligenceIndexVersion json.RawMessage `json:"intelligence_index_version"`
	Pagination               *struct {
		Page       int  `json:"page"`
		TotalPages int  `json:"total_pages"`
		HasMore    bool `json:"has_more"`
	} `json:"pagination"`
	Data []apiModel `json:"data"`
}

type apiModel struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	Slug         string  `json:"slug"`
	ReleaseDate  *string `json:"release_date"`
	ModelCreator *struct {
		Name    string  `json:"name"`
		Country *string `json:"country"`
	} `json:"model_creator"`
	ReasoningModel *bool `json:"reasoning_model"`
	Evaluations    *struct {
		Intelligence *float64 `json:"artificial_analysis_intelligence_index"`
		Coding       *float64 `json:"artificial_analysis_coding_index"`
		Agentic      *float64 `json:"artificial_analysis_agentic_index"`
	} `json:"evaluations"`
	IndexCost *struct {
		TotalCost *float64 `json:"total_cost"`
	} `json:"artificial_analysis_intelligence_index_cost"`
	Pricing *struct {
		Input   *float64 `json:"price_1m_input_tokens"`
		Output  *float64 `json:"price_1m_output_tokens"`
		Blended *float64 `json:"price_1m_blended_3_to_1"`
	} `json:"pricing"`
	Performance *struct {
		OutputTPS *float64 `json:"median_output_tokens_per_second"`
		TTFT      *float64 `json:"median_time_to_first_token_seconds"`
		EndToEnd  *float64 `json:"median_end_to_end_response_time_seconds"`
	} `json:"performance"`
	ContextWindowTokens *float64 `json:"context_window_tokens"`
	Licensing           *struct {
		IsOpenWeights *bool `json:"is_open_weights"`
	} `json:"licensing"`
	OpenRouterAPIID *string `json:"openrouter_api_id"`
}

// endpointPath maps a tier to its models endpoint.
func endpointPath(tier string) string {
	if tier == TierPro {
		return "/language/models"
	}
	return "/language/models/free"
}

// fetchAll reads every page for the tier. The request is bound to ctx; no
// client timeout is set.
func fetchAll(ctx context.Context, client *http.Client, baseURL, apiKey, tier string, now time.Time) (*Snapshot, error) {
	if client == nil {
		client = &http.Client{}
	}
	snap := &Snapshot{FetchedAt: now, Models: []Model{}}
	for page := 1; page <= maxPages; page++ {
		resp, err := fetchPage(ctx, client, baseURL, apiKey, tier, page, now)
		if err != nil {
			return nil, err
		}
		if page == 1 {
			snap.Tier = resp.Tier
			if v := resp.IntelligenceIndexVersion; len(v) > 0 && string(v) != "null" {
				snap.IntelligenceIndexVersion = v
			}
		}
		for i := range resp.Data {
			snap.Models = append(snap.Models, normalize(&resp.Data[i]))
		}
		if resp.Pagination == nil || !resp.Pagination.HasMore || len(resp.Data) == 0 {
			return snap, nil
		}
	}
	log.WithField("pages", maxPages).Warn("model intelligence: pagination limit reached; returning partial data")
	return snap, nil
}

func fetchPage(ctx context.Context, client *http.Client, baseURL, apiKey, tier string, page int, now time.Time) (*apiResponse, error) {
	endpoint := strings.TrimRight(baseURL, "/") + endpointPath(tier)
	if page > 1 {
		endpoint += "?" + url.Values{"page": {strconv.Itoa(page)}}.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("build artificial analysis request: %w", err)
	}
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", UserAgent)
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("artificial analysis request failed: %w", err)
	}
	defer func() {
		if errClose := res.Body.Close(); errClose != nil {
			log.WithError(errClose).Debug("model intelligence: close response body")
		}
	}()
	body, err := io.ReadAll(io.LimitReader(res.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("read artificial analysis response: %w", err)
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil, upstreamError(res, body, now)
	}
	var out apiResponse
	if err = json.Unmarshal(body, &out); err != nil {
		return nil, &UpstreamError{Status: res.StatusCode, Message: fmt.Sprintf("invalid Artificial Analysis response: %v", err)}
	}
	return &out, nil
}

func upstreamError(res *http.Response, body []byte, now time.Time) *UpstreamError {
	e := &UpstreamError{Status: res.StatusCode}
	switch {
	case e.Unauthorized():
		e.Message = "invalid or unauthorized Artificial Analysis API key"
	case e.RateLimited():
		e.RetryAfter = parseRetryAfter(res.Header.Get("Retry-After"), now)
		e.Message = "Artificial Analysis rate limit exceeded"
	default:
		e.Message = fmt.Sprintf("Artificial Analysis returned HTTP %d", res.StatusCode)
		var payload struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		if json.Unmarshal(body, &payload) == nil {
			if detail := strings.TrimSpace(payload.Error + " " + payload.Message); detail != "" {
				if len(detail) > 200 {
					detail = detail[:200]
				}
				e.Message += ": " + detail
			}
		}
	}
	return e
}

func parseRetryAfter(value string, now time.Time) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return seconds
	}
	if at, err := http.ParseTime(value); err == nil {
		if d := at.Sub(now); d > 0 {
			return int(d.Seconds() + 0.5)
		}
	}
	return 0
}

func normalize(m *apiModel) Model {
	out := Model{
		ID:            m.ID,
		Name:          m.Name,
		Slug:          m.Slug,
		ReleaseDate:   nonEmpty(m.ReleaseDate),
		ContextWindow: m.ContextWindowTokens,
		OpenRouterID:  nonEmpty(m.OpenRouterAPIID),
		Reasoning:     m.ReasoningModel != nil && *m.ReasoningModel,
	}
	if m.ModelCreator != nil {
		out.Creator = Creator{Name: m.ModelCreator.Name, Country: nonEmpty(m.ModelCreator.Country)}
	}
	if m.Licensing != nil {
		out.OpenWeights = m.Licensing.IsOpenWeights
	}
	if e := m.Evaluations; e != nil {
		out.IntelligenceIndex, out.CodingIndex, out.AgenticIndex = e.Intelligence, e.Coding, e.Agentic
	}
	if m.IndexCost != nil {
		out.IndexCost = m.IndexCost.TotalCost
	}
	if p := m.Pricing; p != nil {
		out.PriceInput, out.PriceOutput, out.PriceBlended = p.Input, p.Output, p.Blended
		if out.PriceBlended == nil && p.Input != nil && p.Output != nil {
			blended := (3**p.Input + *p.Output) / 4
			out.PriceBlended = &blended
		}
	}
	if p := m.Performance; p != nil {
		out.OutputTokensPerSecond, out.TimeToFirstToken, out.EndToEndSeconds = p.OutputTPS, p.TTFT, p.EndToEnd
	}
	return out
}

func nonEmpty(s *string) *string {
	if s == nil || strings.TrimSpace(*s) == "" {
		return nil
	}
	return s
}

// IsUpstreamError unwraps err into an *UpstreamError.
func IsUpstreamError(err error) (*UpstreamError, bool) {
	var e *UpstreamError
	ok := errors.As(err, &e)
	return e, ok
}
