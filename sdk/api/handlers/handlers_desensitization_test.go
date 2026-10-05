package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/desensitization"
	coreexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
)

func TestDesensitizationFailClosedBeforeAuth(t *testing.T) {
	cfg := config.DefaultDesensitizationConfig()
	cfg.Enabled = true
	cfg.FailClosed = true
	eng := desensitization.Configure(cfg)
	eng.SetTestMaskFailure(fmt.Errorf("injected store failure"))
	t.Cleanup(func() { desensitization.Configure(config.DefaultDesensitizationConfig()) })

	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil)
	req := coreexecutor.Request{Model: "gpt-test", Payload: []byte(`{"messages":[{"role":"user","content":"hi"}]}`)}
	opts := coreexecutor.Options{Metadata: map[string]any{}}
	_, _, errMsg := handler.applyRequestInterceptorsBeforeAuth(context.Background(), "openai", req.Model, "req-fc-1", req, opts, "")
	if errMsg == nil || errMsg.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502 fail_closed, got %#v", errMsg)
	}
	if !errMsg.DirectResponse || !strings.Contains(string(errMsg.Body), "desensitization failed") {
		t.Fatalf("expected direct desensitization error body, got %#v", errMsg)
	}
}

func TestDesensitizationFailClosedAfterAuth(t *testing.T) {
	cfg := config.DefaultDesensitizationConfig()
	cfg.Enabled = true
	cfg.FailClosed = true
	eng := desensitization.Configure(cfg)
	eng.SetTestMaskFailure(fmt.Errorf("injected store failure"))
	t.Cleanup(func() { desensitization.Configure(config.DefaultDesensitizationConfig()) })

	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil)
	out := handler.applyRequestInterceptorsAfterAuth(context.Background(), coreexecutor.RequestAfterAuthInterceptRequest{
		SourceFormat:   sdktranslator.FromString("openai"),
		Model:          "gpt-test",
		RequestedModel: "gpt-test",
		Provider:       "openai",
		AuthKind:       "apikey",
		Body:           []byte(`{"messages":[{"role":"user","content":"hi"}]}`),
		Metadata:       map[string]any{},
	}, "req-fc-2", "")
	if !out.Terminate || out.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected terminate 502, got %#v", out)
	}
	if !strings.Contains(string(out.ResponseBody), "desensitization failed") {
		t.Fatalf("expected fail_closed body, got %q", out.ResponseBody)
	}
}

func TestDesensitizationFailClosedSkipsNonJSON(t *testing.T) {
	cfg := config.DefaultDesensitizationConfig()
	cfg.Enabled = true
	cfg.FailClosed = true
	eng := desensitization.Configure(cfg)
	eng.SetTestMaskFailure(fmt.Errorf("injected store failure"))
	t.Cleanup(func() { desensitization.Configure(config.DefaultDesensitizationConfig()) })

	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil)
	raw := []byte("plain-text-body")
	req := coreexecutor.Request{Model: "gpt-test", Payload: raw}
	opts := coreexecutor.Options{Metadata: map[string]any{}}
	gotReq, _, errMsg := handler.applyRequestInterceptorsBeforeAuth(context.Background(), "openai", req.Model, "req-fc-3", req, opts, "")
	if errMsg != nil {
		t.Fatalf("non-JSON must not fail closed: %#v", errMsg)
	}
	if string(gotReq.Payload) != string(raw) {
		t.Fatalf("payload changed: %q", gotReq.Payload)
	}
}

func TestDesensitizationAfterAuthTargetsSelectedProvider(t *testing.T) {
	cfg := config.DefaultDesensitizationConfig()
	cfg.Enabled = true
	cfg.Scope = config.DesensitizationScopeTargeted
	cfg.OAuthProviders = []string{"codex"}
	desensitization.Configure(cfg)
	t.Cleanup(func() { desensitization.Configure(config.DefaultDesensitizationConfig()) })

	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil)
	if handler.requestAfterAuthInterceptor(nil, "req-t", "") == nil {
		t.Fatal("after-auth interceptor must be installed while Privacy is enabled")
	}
	body := []byte(`{"input":"mail alice@example.com"}`)
	intercept := func(provider string) coreexecutor.RequestAfterAuthInterceptResponse {
		return handler.applyRequestInterceptorsAfterAuth(context.Background(), coreexecutor.RequestAfterAuthInterceptRequest{
			SourceFormat: sdktranslator.FromString("openai-response"),
			Model:        "gpt-test",
			Provider:     provider,
			AuthKind:     "oauth",
			Body:         body,
			Metadata:     map[string]any{},
		}, "req-t", "")
	}
	if out := intercept("claude"); len(out.Body) != 0 {
		t.Fatalf("non-targeted provider must not be masked: %s", out.Body)
	}
	out := intercept("codex")
	if len(out.Body) == 0 || strings.Contains(string(out.Body), "alice@example.com") || !strings.Contains(string(out.Body), "{{EMAIL_") {
		t.Fatalf("targeted provider must be masked: %s", out.Body)
	}
}

func TestDesensitizationInterceptorAbsentWhenDisabled(t *testing.T) {
	desensitization.Configure(config.DefaultDesensitizationConfig())
	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil)
	if handler.requestAfterAuthInterceptor(nil, "req-off", "") != nil {
		t.Fatal("after-auth interceptor must stay nil when Privacy is disabled and no plugins run")
	}
	raw := []byte(`{"input":"mail alice@example.com"}`)
	gotReq, _, errMsg := handler.applyRequestInterceptorsBeforeAuth(context.Background(), "openai", "gpt-test", "req-off", coreexecutor.Request{Model: "gpt-test", Payload: raw}, coreexecutor.Options{}, "")
	if errMsg != nil || string(gotReq.Payload) != string(raw) {
		t.Fatalf("disabled Privacy changed payload: %q %#v", gotReq.Payload, errMsg)
	}
}

func TestDesensitizationMaskThenRestoreNonStream(t *testing.T) {
	cfg := config.DefaultDesensitizationConfig()
	cfg.Enabled = true
	desensitization.Configure(cfg)
	t.Cleanup(func() { desensitization.Configure(config.DefaultDesensitizationConfig()) })

	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, nil)
	opts := coreexecutor.Options{Metadata: map[string]any{coreexecutor.CanonicalSessionIDMetadataKey: "session:privacy-test"}}
	req := coreexecutor.Request{Model: "claude-test", Payload: []byte(`{"messages":[{"role":"user","content":"reply to bob@example.com"}]}`)}
	maskedReq, maskedOpts, errMsg := handler.applyRequestInterceptorsBeforeAuth(context.Background(), "claude", req.Model, "req-rt", req, opts, "")
	if errMsg != nil {
		t.Fatalf("unexpected error: %#v", errMsg)
	}
	masked := string(maskedReq.Payload)
	if strings.Contains(masked, "bob@example.com") || string(maskedOpts.OriginalRequest) != masked {
		t.Fatalf("payload not masked consistently: %s / %s", masked, maskedOpts.OriginalRequest)
	}
	start := strings.Index(masked, "{{EMAIL_")
	if start < 0 {
		t.Fatalf("no EMAIL placeholder in %s", masked)
	}
	token := masked[start : start+strings.Index(masked[start:], "}}")+2]
	upstream := []byte(`{"content":[{"type":"text","text":"Sent to ` + token + `"}]}`)
	body, _ := handler.applyResponseInterceptors(context.Background(), "req-rt", "claude", req.Model, req.Model, opts, http.Header{}, http.Header{}, nil, nil, upstream, http.StatusOK, "", false)
	if !strings.Contains(string(body), "bob@example.com") {
		t.Fatalf("response placeholder not restored: %s", body)
	}
}
