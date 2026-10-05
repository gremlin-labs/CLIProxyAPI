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
	"github.com/router-for-me/CLIProxyAPI/v8/internal/desensitization"
)

func TestManagementDesensitizationRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { desensitization.Configure(config.DefaultDesensitizationConfig()) })

	path := filepath.Join(t.TempDir(), "config.yaml")
	raw := "port: 8317\napi-keys: [client]\nremote-management: {secret-key: test-password}\n"
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg.AuthDir = t.TempDir()
	h := management.NewHandler(cfg, path, nil)
	h.SetLocalPassword("test-password")
	s := &Server{cfg: cfg, engine: gin.New(), mgmt: h}
	s.managementRoutesEnabled.Store(true)
	s.registerManagementRoutes()

	routes := make(map[string]bool)
	for _, route := range s.engine.Routes() {
		routes[route.Method+" "+route.Path] = true
	}
	for _, route := range []string{
		"GET /v0/management/desensitization-config",
		"PUT /v0/management/desensitization-config",
		"PATCH /v0/management/desensitization-config",
		"GET /v0/management/desensitization/scope-options",
		"POST /v0/management/desensitization/preview",
		"GET /v8/management/requests/desensitization/scope-options",
		"POST /v8/management/requests/desensitization/preview",
	} {
		if !routes[route] {
			t.Errorf("missing route %s", route)
		}
	}

	request := func(method, url, body string, status int) string {
		t.Helper()
		req := httptest.NewRequest(method, url, strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:1234"
		req.Header.Set("Authorization", "Bearer test-password")
		req.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		s.engine.ServeHTTP(response, req)
		if response.Code != status {
			t.Fatalf("%s %s: status=%d body=%s", method, url, response.Code, response.Body.String())
		}
		return response.Body.String()
	}

	var got config.DesensitizationConfig
	if err := json.Unmarshal([]byte(request(http.MethodGet, "/v0/management/desensitization-config", "", http.StatusOK)), &got); err != nil {
		t.Fatal(err)
	}
	if got.Enabled || got.Scope != config.DesensitizationScopeAll || got.Restore == nil || !*got.Restore {
		t.Fatalf("unexpected defaults: %+v", got)
	}

	request(http.MethodPut, "/v0/management/desensitization-config", `{"enabled":true,"scope":"targeted","oauth_providers":["codex"]}`, http.StatusOK)
	if eng := desensitization.Current(); eng == nil || !eng.Config().Enabled {
		t.Fatal("PUT did not reconfigure the live engine")
	}
	if !cfg.Desensitization.Applies("", "codex", "oauth") || cfg.Desensitization.Applies("", "claude", "oauth") {
		t.Fatalf("targeted scope not stored: %+v", cfg.Desensitization)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), "desensitization:") {
		t.Fatalf("config not persisted:\n%s", saved)
	}

	v8Value := request(http.MethodGet, "/v8/management/config/requests/desensitization/enabled", "", http.StatusOK)
	if strings.TrimSpace(v8Value) != "true" {
		t.Fatalf("v8 config path = %q", v8Value)
	}

	var preview struct {
		Masked string `json:"masked"`
		Hits   []struct {
			Category string `json:"category"`
			Count    int    `json:"count"`
		} `json:"hits"`
	}
	body := request(http.MethodPost, "/v8/management/requests/desensitization/preview", `{"text":"mail alice@example.com"}`, http.StatusOK)
	if err := json.Unmarshal([]byte(body), &preview); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(preview.Masked, "alice@example.com") || len(preview.Hits) != 1 || preview.Hits[0].Category != "EMAIL" {
		t.Fatalf("unexpected preview: %s", body)
	}
}
