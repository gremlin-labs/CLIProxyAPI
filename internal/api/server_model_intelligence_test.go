package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/api/handlers/management"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"gopkg.in/yaml.v3"
)

// TestModelIntelligenceRoutes checks the v0 and v8 registrations and that a v8
// PUT persists the key under observability.model-intelligence without echoing it.
func TestModelIntelligenceRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("ARTIFICIAL_ANALYSIS_API_KEY", "")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("config-version: 8\nserver:\n  port: 8317\nmanagement:\n  secret-key: test-password\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Plugins.Dir = filepath.Dir(path)
	h := management.NewHandler(cfg, path, nil)
	h.SetLocalPassword("test-password")
	s := &Server{cfg: cfg, engine: gin.New(), mgmt: h}
	s.managementRoutesEnabled.Store(true)
	s.registerManagementRoutes()

	do := func(method, route, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, route, strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:1234"
		req.Header.Set("Authorization", "Bearer test-password")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		s.engine.ServeHTTP(rec, req)
		return rec
	}
	for _, route := range []string{"/v0/management/model-intelligence", "/v8/management/observability/model-intelligence"} {
		rec := do(http.MethodGet, route, "")
		var body map[string]any
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &body) != nil || body["configured"] != false {
			t.Fatalf("%s: status=%d body=%s", route, rec.Code, rec.Body.String())
		}
	}
	if rec := do(http.MethodPut, "/v0/management/model-intelligence/config", `{"tier":"bogus"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("v0 PUT: status=%d body=%s", rec.Code, rec.Body.String())
	}

	const key = "aa-route-secret"
	rec := do(http.MethodPut, "/v8/management/observability/model-intelligence/config", `{"api_key":"`+key+`"}`)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), key) {
		t.Fatalf("v8 PUT: status=%d body=%s", rec.Code, rec.Body.String())
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Observability struct {
			ModelIntelligence map[string]string `yaml:"model-intelligence"`
		} `yaml:"observability"`
	}
	if errDecode := yaml.Unmarshal(saved, &doc); errDecode != nil || doc.Observability.ModelIntelligence["artificial-analysis-api-key"] != key {
		t.Fatalf("v8 PUT did not save under observability.model-intelligence:\n%s", saved)
	}
}
