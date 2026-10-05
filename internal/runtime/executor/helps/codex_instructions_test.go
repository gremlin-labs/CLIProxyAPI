package helps

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/tidwall/gjson"
)

func codexInstructionsTestConfig(mode string, models ...string) *config.Config {
	return &config.Config{Codex: config.CodexConfig{Instructions: config.CodexInstructionsConfig{
		Enabled: true,
		Mode:    mode,
		Content: "EXTRA",
		Models:  models,
	}}}
}

func oauthAuth() *cliproxyauth.Auth {
	return &cliproxyauth.Auth{Provider: "codex", Metadata: map[string]any{"type": "codex"}}
}

func TestApplyCodexConfiguredInstructionsModes(t *testing.T) {
	body := []byte(`{"instructions":"CLIENT","input":"hi"}`)
	cases := map[string]string{
		"":        "EXTRA\n\nCLIENT",
		"prepend": "EXTRA\n\nCLIENT",
		"append":  "CLIENT\n\nEXTRA",
		"replace": "EXTRA",
	}
	for mode, want := range cases {
		out := ApplyCodexConfiguredInstructions(codexInstructionsTestConfig(mode), oauthAuth(), "gpt-5.5", body)
		if got := gjson.GetBytes(out, "instructions").String(); got != want {
			t.Errorf("mode %q: instructions = %q, want %q", mode, got, want)
		}
	}
}

func TestApplyCodexConfiguredInstructionsModelPatterns(t *testing.T) {
	body := []byte(`{"instructions":"CLIENT"}`)
	cases := []struct {
		models []string
		model  string
		want   bool
	}{
		{nil, "o3", true},
		{[]string{"gpt-5*"}, "gpt-5.5", true},
		{[]string{"gpt-5*"}, "team/gpt-5.5", true},
		{[]string{"gpt-5*"}, "o3", false},
		{[]string{"gpt-5.5"}, "gpt-5.5-codex", false},
		{[]string{"*-codex"}, "gpt-5.5-codex", true},
	}
	for _, tc := range cases {
		out := ApplyCodexConfiguredInstructions(codexInstructionsTestConfig("replace", tc.models...), oauthAuth(), tc.model, body)
		if applied := gjson.GetBytes(out, "instructions").String() == "EXTRA"; applied != tc.want {
			t.Errorf("models %v, model %q: applied = %v, want %v", tc.models, tc.model, applied, tc.want)
		}
	}
}

func TestApplyCodexConfiguredInstructionsSkips(t *testing.T) {
	body := []byte(`{"instructions":"CLIENT"}`)
	apiKeyAuth := &cliproxyauth.Auth{Provider: "codex", Attributes: map[string]string{"api_key": "sk-test"}}
	disabled := codexInstructionsTestConfig("")
	disabled.Codex.Instructions.Enabled = false

	cases := []struct {
		name string
		cfg  *config.Config
		auth *cliproxyauth.Auth
	}{
		{"disabled", disabled, oauthAuth()},
		{"api key with oauth-only", codexInstructionsTestConfig(""), apiKeyAuth},
		{"nil auth with oauth-only", codexInstructionsTestConfig(""), nil},
		{"nil config", nil, oauthAuth()},
	}
	for _, tc := range cases {
		out := ApplyCodexConfiguredInstructions(tc.cfg, tc.auth, "gpt-5.5", body)
		if got := gjson.GetBytes(out, "instructions").String(); got != "CLIENT" {
			t.Errorf("%s: instructions = %q, want unchanged", tc.name, got)
		}
	}

	oauthOff := false
	cfg := codexInstructionsTestConfig("replace")
	cfg.Codex.Instructions.OAuthOnly = &oauthOff
	out := ApplyCodexConfiguredInstructions(cfg, apiKeyAuth, "gpt-5.5", body)
	if got := gjson.GetBytes(out, "instructions").String(); got != "EXTRA" {
		t.Fatalf("oauth-only=false on api key: instructions = %q, want EXTRA", got)
	}
}

func TestApplyCodexConfiguredInstructionsReadsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "instructions.md")
	if errWrite := os.WriteFile(path, []byte("  FROM FILE \n"), 0o600); errWrite != nil {
		t.Fatalf("write instructions file: %v", errWrite)
	}
	cfg := codexInstructionsTestConfig("replace")
	cfg.Codex.Instructions.Content = ""
	cfg.Codex.Instructions.File = path
	out := ApplyCodexConfiguredInstructions(cfg, oauthAuth(), "gpt-5.5", []byte(`{"instructions":""}`))
	if got := gjson.GetBytes(out, "instructions").String(); got != "FROM FILE" {
		t.Fatalf("instructions = %q, want %q", got, "FROM FILE")
	}
}
