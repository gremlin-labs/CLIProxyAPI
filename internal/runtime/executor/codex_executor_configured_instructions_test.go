package executor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	"github.com/tidwall/gjson"
)

func codexConfiguredInstructionsServer(t *testing.T, gotBody *[]byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		*gotBody = body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"object\":\"response\",\"created_at\":0,\"status\":\"completed\",\"background\":false,\"error\":null}}\n\n"))
	}))
}

func codexConfiguredInstructionsAuth(baseURL string) *cliproxyauth.Auth {
	return &cliproxyauth.Auth{
		Provider:   "codex",
		Attributes: map[string]string{"base_url": baseURL},
		Metadata: map[string]any{
			"type": "codex", "access_token": "oauth-token", "account_id": "acct",
		},
	}
}

func executeCodexConfiguredInstructions(t *testing.T, cfg *config.Config) []byte {
	t.Helper()
	var gotBody []byte
	server := codexConfiguredInstructionsServer(t, &gotBody)
	defer server.Close()
	_, err := NewCodexExecutor(cfg).Execute(context.Background(), codexConfiguredInstructionsAuth(server.URL), cliproxyexecutor.Request{
		Model:   "gpt-5.5",
		Payload: []byte(`{"model":"gpt-5.5","instructions":"CLIENT","input":"hello"}`),
	}, cliproxyexecutor.Options{
		SourceFormat: sdktranslator.FromString("openai-response"),
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	return gotBody
}

func configuredInstructionsExecutorConfig() *config.Config {
	return &config.Config{
		SDKConfig: config.SDKConfig{DisableImageGeneration: config.DisableImageGenerationAll},
		Codex: config.CodexConfig{Instructions: config.CodexInstructionsConfig{
			Enabled: true,
			Content: "EXTRA",
		}},
	}
}

func TestCodexExecutorInjectsConfiguredInstructions(t *testing.T) {
	cfg := configuredInstructionsExecutorConfig()
	body := executeCodexConfiguredInstructions(t, cfg)
	if got := gjson.GetBytes(body, "instructions").String(); got != "EXTRA\n\nCLIENT" {
		t.Fatalf("instructions = %q, want configured instructions prepended", got)
	}

	cfg.Codex.Instructions.Enabled = false
	body = executeCodexConfiguredInstructions(t, cfg)
	if got := gjson.GetBytes(body, "instructions").String(); got != "CLIENT" {
		t.Fatalf("disabled instructions = %q, want unchanged", got)
	}
}

// Payload rules stay the final barrier: an override of instructions wins over the
// injected configured instructions.
func TestCodexExecutorPayloadOverrideWinsOverConfiguredInstructions(t *testing.T) {
	cfg := configuredInstructionsExecutorConfig()
	cfg.Payload = config.PayloadConfig{Override: []config.PayloadRule{{
		Models: []config.PayloadModelRule{{Name: "gpt-5.5"}},
		Params: map[string]any{"instructions": "OVERRIDE"},
	}}}
	body := executeCodexConfiguredInstructions(t, cfg)
	if got := gjson.GetBytes(body, "instructions").String(); got != "OVERRIDE" {
		t.Fatalf("instructions = %q, want payload override", got)
	}
}
