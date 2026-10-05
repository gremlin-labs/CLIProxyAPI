package helps

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/codexinstructions"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/tidwall/gjson"
)

func codexInstructionsTestConfig(mode string) *config.Config {
	return &config.Config{Codex: config.CodexConfig{Instructions: config.CodexInstructionsConfig{
		Enabled: true,
		Mode:    mode,
		Content: "PRIVATE",
		Models:  []string{"gpt-5*"},
	}}}
}

func markedOAuthAuth() *cliproxyauth.Auth {
	return &cliproxyauth.Auth{
		Provider: "codex",
		Metadata: map[string]any{"type": "codex", codexinstructions.AuthMetadataKey: true},
	}
}

var privateMeta = map[string]any{codexinstructions.RequestPrivateMetadataKey: true}

func TestApplyCodexConfiguredInstructionsModes(t *testing.T) {
	body := []byte(`{"instructions":"CLIENT","input":"hi"}`)
	cases := map[string]string{
		"":        "PRIVATE\n\nCLIENT",
		"prepend": "PRIVATE\n\nCLIENT",
		"append":  "CLIENT\n\nPRIVATE",
		"replace": "PRIVATE",
	}
	for mode, want := range cases {
		out := ApplyCodexConfiguredInstructions(codexInstructionsTestConfig(mode), markedOAuthAuth(), "gpt-5.5", body, privateMeta)
		if got := gjson.GetBytes(out, "instructions").String(); got != want {
			t.Errorf("mode %q: instructions = %q, want %q", mode, got, want)
		}
	}
}

func TestApplyCodexConfiguredInstructionsSkips(t *testing.T) {
	body := []byte(`{"instructions":"CLIENT"}`)
	apiKeyAuth := &cliproxyauth.Auth{Provider: "codex", Attributes: map[string]string{
		"api_key": "sk-test", codexinstructions.AuthAttributeKey: "true",
	}}
	unmarked := &cliproxyauth.Auth{Provider: "codex", Metadata: map[string]any{"type": "codex"}}
	disabled := codexInstructionsTestConfig("")
	disabled.Codex.Instructions.Enabled = false

	cases := []struct {
		name  string
		cfg   *config.Config
		auth  *cliproxyauth.Auth
		model string
		meta  map[string]any
	}{
		{"disabled", disabled, markedOAuthAuth(), "gpt-5.5", privateMeta},
		{"request not private", codexInstructionsTestConfig(""), markedOAuthAuth(), "gpt-5.5", nil},
		{"model not eligible", codexInstructionsTestConfig(""), markedOAuthAuth(), "o3", privateMeta},
		{"api key with oauth-only", codexInstructionsTestConfig(""), apiKeyAuth, "gpt-5.5", privateMeta},
		{"unmarked auth", codexInstructionsTestConfig(""), unmarked, "gpt-5.5", privateMeta},
		{"nil config", nil, markedOAuthAuth(), "gpt-5.5", privateMeta},
	}
	for _, tc := range cases {
		out := ApplyCodexConfiguredInstructions(tc.cfg, tc.auth, tc.model, body, tc.meta)
		if got := gjson.GetBytes(out, "instructions").String(); got != "CLIENT" {
			t.Errorf("%s: instructions = %q, want unchanged", tc.name, got)
		}
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
	out := ApplyCodexConfiguredInstructions(cfg, markedOAuthAuth(), "gpt-5.5", []byte(`{"instructions":""}`), privateMeta)
	if got := gjson.GetBytes(out, "instructions").String(); got != "FROM FILE" {
		t.Fatalf("instructions = %q, want %q", got, "FROM FILE")
	}
}
