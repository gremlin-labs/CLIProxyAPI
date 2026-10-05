package management

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
)

func serveCodexConfig(t *testing.T, handler gin.HandlerFunc, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	ctx.Request = httptest.NewRequest(method, "/v0/management/codex", strings.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	handler(ctx)
	return rec
}

func TestCodexInstructionsRoundTrip(t *testing.T) {
	cfg := &config.Config{}
	path := writeTestConfigFile(t)
	h := &Handler{cfg: cfg, configFilePath: path}

	// The panel still sends the removed marker/segregation fields; they are ignored.
	rec := serveCodexConfig(t, h.PutCodexInstructions, http.MethodPut, `{
		"enabled": true, "mode": " Append ", "content": "Be terse.", "file": "",
		"models": [" gpt-5.5 ", ""], "oauth-only": false, "require-auth-allow": false,
		"reserve-marked-auths": true, "use-prefix-suffix": true,
		"request-markers": {"prefixes": ["private/"], "suffixes": []}
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body=%s", rec.Code, rec.Body.String())
	}
	got := cfg.Codex.Instructions
	if !got.Enabled || got.Mode != "append" || got.Content != "Be terse." || len(got.Models) != 1 || got.Models[0] != "gpt-5.5" || got.OAuthOnlyEnabled() {
		t.Fatalf("stored instructions = %+v", got)
	}

	saved, errRead := os.ReadFile(path)
	if errRead != nil {
		t.Fatalf("read saved config: %v", errRead)
	}
	for _, removed := range []string{"request-markers", "reserve-marked-auths", "require-auth-allow", "use-prefix-suffix", "private/"} {
		if strings.Contains(string(saved), removed) {
			t.Fatalf("saved config contains removed field %q:\n%s", removed, saved)
		}
	}
	reloaded, errLoad := config.LoadConfig(path)
	if errLoad != nil {
		t.Fatalf("reload saved config: %v", errLoad)
	}
	if reloaded.Codex.Instructions.Content != "Be terse." || reloaded.Codex.Instructions.Mode != "append" {
		t.Fatalf("reloaded instructions = %+v\nsaved:\n%s", reloaded.Codex.Instructions, saved)
	}

	rec = serveCodexConfig(t, h.GetCodexInstructions, http.MethodGet, "")
	var body map[string]any
	if errDecode := json.Unmarshal(rec.Body.Bytes(), &body); errDecode != nil {
		t.Fatalf("decode GET body: %v", errDecode)
	}
	if body["mode"] != "append" || body["oauth-only"] != false {
		t.Fatalf("GET body = %v", body)
	}
	for _, removed := range []string{"request-markers", "reserve-marked-auths", "require-auth-allow", "use-prefix-suffix"} {
		if _, ok := body[removed]; ok {
			t.Fatalf("GET body returns removed field %q: %v", removed, body)
		}
	}
}

func TestPutCodexInstructionsRejectsUnknownMode(t *testing.T) {
	h := &Handler{cfg: &config.Config{}, configFilePath: writeTestConfigFile(t)}
	rec := serveCodexConfig(t, h.PutCodexInstructions, http.MethodPut, `{"enabled":true,"mode":"merge"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestPutCodexRoutingConfigPreservesOtherCodexSettings(t *testing.T) {
	autoDisable := true
	cfg := &config.Config{Codex: config.CodexConfig{
		ResponseSteering:        true,
		AutoDisableAuthFailures: &autoDisable,
	}}
	h := &Handler{cfg: cfg, configFilePath: writeTestConfigFile(t)}
	rec := serveCodexConfig(t, h.PutCodexRoutingConfig, http.MethodPut, `{"strategy":"Adaptive","prefer-free-for-shared-models":true}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if cfg.Codex.Routing.Strategy != "adaptive" || !cfg.Codex.Routing.PreferFreeForSharedModels {
		t.Fatalf("routing = %+v", cfg.Codex.Routing)
	}
	if !cfg.Codex.ResponseSteering || cfg.Codex.AutoDisableAuthFailures == nil || !*cfg.Codex.AutoDisableAuthFailures {
		t.Fatal("updating Codex routing replaced unrelated Codex settings")
	}

	rec = serveCodexConfig(t, h.GetCodexRoutingConfig, http.MethodGet, "")
	if !strings.Contains(rec.Body.String(), `"prefer-free-for-shared-models":true`) {
		t.Fatalf("GET body = %s", rec.Body.String())
	}
}

func TestPutCodexRoutingConfigRejectsUnknownStrategy(t *testing.T) {
	h := &Handler{cfg: &config.Config{}, configFilePath: writeTestConfigFile(t)}
	rec := serveCodexConfig(t, h.PutCodexRoutingConfig, http.MethodPut, `{"strategy":"round-robin"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
}

func TestCodexFailureConfigDefaultsAndPartialUpdate(t *testing.T) {
	cfg := &config.Config{Codex: config.CodexConfig{ResponseSteering: true}}
	h := &Handler{cfg: cfg, configFilePath: writeTestConfigFile(t)}

	rec := serveCodexConfig(t, h.GetCodexFailureConfig, http.MethodGet, "")
	var defaults map[string]any
	if errDecode := json.Unmarshal(rec.Body.Bytes(), &defaults); errDecode != nil {
		t.Fatalf("decode GET body: %v", errDecode)
	}
	if defaults["auto-disable-auth-failures"] != false || defaults["auth-failure-disable-after"] != float64(1) ||
		defaults["usage-limit-disable-after"] != float64(0) || defaults["usage-limit-cooldown-fallback-hours"] != float64(0) {
		t.Fatalf("default failure config = %v, want every policy off", defaults)
	}

	rec = serveCodexConfig(t, h.PutCodexFailureConfig, http.MethodPut, `{"auto-disable-auth-failures":true,"usage-limit-disable-after":-4}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT status = %d, body=%s", rec.Code, rec.Body.String())
	}
	policy := cfg.Codex.FailurePolicy()
	if !policy.AutoDisableAuthFailures || policy.AuthFailureDisableAfter != 1 || policy.UsageLimitDisableAfter != 0 || policy.UsageLimitCooldownFallbackHours != 0 {
		t.Fatalf("policy = %+v", policy)
	}

	rec = serveCodexConfig(t, h.PutCodexFailureConfig, http.MethodPatch, `{"usage-limit-cooldown-fallback-hours":2}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH status = %d, body=%s", rec.Code, rec.Body.String())
	}
	policy = cfg.Codex.FailurePolicy()
	if !policy.AutoDisableAuthFailures || policy.UsageLimitCooldownFallbackHours != 2 || !cfg.Codex.ResponseSteering {
		t.Fatalf("partial update lost settings: policy=%+v steering=%v", policy, cfg.Codex.ResponseSteering)
	}
}
