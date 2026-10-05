package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/api/handlers/management"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/usagestore"
)

// TestUsageMonitoringRoutesV0AndV8 checks that the request monitoring routes the
// management panel calls are registered under /v0 and the v8 observability group,
// and that both serve the same durable store.
func TestUsageMonitoringRoutesV0AndV8(t *testing.T) {
	gin.SetMode(gin.TestMode)
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("port: 8317\nusage-statistics-enabled: true\nremote-management: {secret-key: test-password}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	store, err := usagestore.Open(usagestore.Options{Path: filepath.Join(dir, "usage.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if errClose := store.Close(); errClose != nil {
			t.Errorf("close store: %v", errClose)
		}
	}()
	now := time.Now().UnixMilli()
	for _, event := range []usagestore.Event{
		{TimestampMS: now - 1000, Provider: "claude", Model: "claude-sonnet-4-5", Source: "a@example.com", InputTokens: 1_000_000},
		{TimestampMS: now, Provider: "codex", Model: "gpt-5-codex", Failed: true, FailStatusCode: http.StatusTooManyRequests},
	} {
		if errInsert := store.Insert(context.Background(), event); errInsert != nil {
			t.Fatal(errInsert)
		}
	}

	h := management.NewHandler(cfg, path, nil)
	h.SetLocalPassword("test-password")
	h.SetUsageStore(store)
	s := &Server{cfg: cfg, engine: gin.New(), mgmt: h}
	s.managementRoutesEnabled.Store(true)
	s.registerManagementRoutes()

	routes := make(map[string]bool)
	for _, route := range s.engine.Routes() {
		routes[route.Method+" "+route.Path] = true
	}
	for _, route := range []string{
		"GET /v0/management/usage-statistics-enabled", "PUT /v0/management/usage-statistics-enabled", "PATCH /v0/management/usage-statistics-enabled",
		"GET /v0/management/usage-retention-days", "PUT /v0/management/usage-retention-days",
		"GET /v0/management/api-key-usage", "GET /v0/management/usage-queue",
		"GET /v0/management/usage-events", "POST /v0/management/usage-events",
		"GET /v0/management/usage-summary", "POST /v0/management/usage-summary",
		"GET /v0/management/usage-filter-options", "POST /v0/management/usage-filter-options",
		"GET /v0/management/usage-account-stats", "POST /v0/management/usage-account-stats",
		"POST /v0/management/usage-account-recent-requests",
		"GET /v0/management/usage-api-key-stats", "POST /v0/management/usage-api-key-stats",
		"GET /v0/management/model-prices", "PUT /v0/management/model-prices", "PATCH /v0/management/model-prices", "DELETE /v0/management/model-prices",
		"PUT /v0/management/model-price-aliases", "PATCH /v0/management/model-price-aliases", "DELETE /v0/management/model-price-aliases",
		"POST /v0/management/model-prices/sync",
		"GET /v8/management/observability/usage/retention-days", "PUT /v8/management/observability/usage/retention-days",
		"POST /v8/management/observability/usage/events", "POST /v8/management/observability/usage/summary",
		"POST /v8/management/observability/usage/filter-options", "POST /v8/management/observability/usage/accounts",
		"POST /v8/management/observability/usage/accounts/recent-requests", "POST /v8/management/observability/usage/api-key-stats",
		"GET /v8/management/observability/usage/model-prices", "PUT /v8/management/observability/usage/model-prices",
		"DELETE /v8/management/observability/usage/model-prices", "POST /v8/management/observability/usage/model-prices/sync",
		"PUT /v8/management/observability/usage/model-price-aliases", "DELETE /v8/management/observability/usage/model-price-aliases",
	} {
		if !routes[route] {
			t.Errorf("missing route %s", route)
		}
	}

	call := func(method, url, body string) []byte {
		t.Helper()
		req := httptest.NewRequest(method, url, strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:1234"
		req.Header.Set("Authorization", "Bearer test-password")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.engine.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s %s: status=%d body=%s", method, url, rec.Code, rec.Body.String())
		}
		return rec.Body.Bytes()
	}

	call(http.MethodPut, "/v0/management/model-prices", `{"prices":[{"model":"claude-sonnet-4-5","prompt_per_1m":3,"completion_per_1m":15}]}`)
	for _, url := range []string{"/v0/management/usage-summary", "/v8/management/observability/usage/summary"} {
		var response struct {
			Summary                usagestore.Summary `json:"summary"`
			UsageStatisticsEnabled bool               `json:"usage_statistics_enabled"`
		}
		if errDecode := json.Unmarshal(call(http.MethodPost, url, `{}`), &response); errDecode != nil {
			t.Fatal(errDecode)
		}
		if response.Summary.TotalCalls != 2 || response.Summary.FailureCalls != 1 || response.Summary.PricedCalls != 1 ||
			response.Summary.EstimatedCost != 3 || !response.UsageStatisticsEnabled {
			t.Fatalf("%s summary = %+v enabled=%v", url, response.Summary, response.UsageStatisticsEnabled)
		}
	}

	var prices struct {
		Prices   []usagestore.ModelPrice `json:"prices"`
		Unpriced []string                `json:"unpriced_models"`
	}
	if errDecode := json.Unmarshal(call(http.MethodGet, "/v0/management/model-prices", ""), &prices); errDecode != nil {
		t.Fatal(errDecode)
	}
	if len(prices.Prices) != 1 || len(prices.Unpriced) != 1 || prices.Unpriced[0] != "gpt-5-codex" {
		t.Fatalf("model prices = %+v", prices)
	}

	var events struct {
		Events []usagestore.Event `json:"events"`
	}
	if errDecode := json.Unmarshal(call(http.MethodPost, "/v0/management/usage-events", `{"providers":["claude"]}`), &events); errDecode != nil {
		t.Fatal(errDecode)
	}
	if len(events.Events) != 1 || events.Events[0].EstimatedCost == nil || *events.Events[0].EstimatedCost != 3 {
		t.Fatalf("filtered events = %+v", events.Events)
	}
}

func TestUsageMonitoringRoutesWithoutStore(t *testing.T) {
	gin.SetMode(gin.TestMode)
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("port: 8317\nremote-management: {secret-key: test-password}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	h := management.NewHandler(cfg, path, nil)
	h.SetLocalPassword("test-password")
	s := &Server{cfg: cfg, engine: gin.New(), mgmt: h}
	s.managementRoutesEnabled.Store(true)
	s.registerManagementRoutes()
	req := httptest.NewRequest(http.MethodPost, "/v0/management/usage-summary", strings.NewReader(`{}`))
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("Authorization", "Bearer test-password")
	rec := httptest.NewRecorder()
	s.engine.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s, want 503 when the usage store is unavailable", rec.Code, rec.Body.String())
	}
}
