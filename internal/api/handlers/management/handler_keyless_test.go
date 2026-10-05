package management

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
)

func keylessStatus(t *testing.T, cfg *config.Config, remoteAddr, origin string) int {
	t.Helper()
	h := &Handler{cfg: cfg, failedAttempts: make(map[string]*attemptInfo)}
	engine := gin.New()
	engine.GET("/v0/management/config", h.Middleware(), func(c *gin.Context) { c.Status(http.StatusOK) })
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v0/management/config", nil)
	req.RemoteAddr = remoteAddr
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	engine.ServeHTTP(rec, req)
	return rec.Code
}

func TestMiddlewareLocalWithoutKey(t *testing.T) {
	keyless := &config.Config{}
	keyless.RemoteManagement.LocalWithoutKey = true

	for name, tc := range map[string]struct {
		cfg        *config.Config
		remoteAddr string
		origin     string
		want       int
	}{
		"local, no origin":           {keyless, "127.0.0.1:5000", "", http.StatusOK},
		"local, localhost page":      {keyless, "127.0.0.1:5000", "http://localhost:5183", http.StatusOK},
		"local, loopback ipv6":       {keyless, "[::1]:5000", "http://127.0.0.1:8317", http.StatusOK},
		"local, other website":       {keyless, "127.0.0.1:5000", "https://example.com", http.StatusForbidden},
		"local, rebinding lookalike": {keyless, "127.0.0.1:5000", "http://localhost.example.com", http.StatusForbidden},
		"remote":                     {keyless, "192.0.2.10:5000", "", http.StatusForbidden},
		"option off":                 {&config.Config{}, "127.0.0.1:5000", "", http.StatusForbidden},
	} {
		if got := keylessStatus(t, tc.cfg, tc.remoteAddr, tc.origin); got != tc.want {
			t.Errorf("%s: status = %d, want %d", name, got, tc.want)
		}
	}

	// A configured key still applies: keyless mode only covers the no-key setup.
	keyed := &config.Config{}
	keyed.RemoteManagement.LocalWithoutKey = true
	keyed.RemoteManagement.SecretKey = "set"
	if got := keylessStatus(t, keyed, "127.0.0.1:5000", ""); got != http.StatusUnauthorized {
		t.Errorf("with a secret key: status = %d, want %d", got, http.StatusUnauthorized)
	}
}
