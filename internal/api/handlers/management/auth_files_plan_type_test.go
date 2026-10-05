package management

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func TestListAuthFiles_IncludesCodexPlanTypeAndDisabledReason(t *testing.T) {
	t.Setenv("MANAGEMENT_PASSWORD", "")

	authDir := t.TempDir()
	fileName := "codex-user@example.com.json"
	filePath := filepath.Join(authDir, fileName)
	if errWrite := os.WriteFile(filePath, []byte(`{"type":"codex"}`), 0o600); errWrite != nil {
		t.Fatalf("failed to write auth file: %v", errWrite)
	}

	manager := coreauth.NewManager(nil, nil, nil)
	record := &coreauth.Auth{
		ID:         fileName,
		FileName:   fileName,
		Provider:   "codex",
		Status:     coreauth.StatusActive,
		Attributes: map[string]string{"path": filePath, "plan_type": "plus"},
		Metadata: map[string]any{
			"type":            "codex",
			"plan_type":       "pro",
			"disabled_reason": "token revoked",
		},
	}
	if _, errRegister := manager.Register(context.Background(), record); errRegister != nil {
		t.Fatalf("failed to register auth record: %v", errRegister)
	}

	h := NewHandlerWithoutConfigFilePath(&config.Config{AuthDir: authDir}, manager)
	h.tokenStore = &memoryAuthStore{}

	entry := firstAuthFileEntry(t, h)
	if got := entry["plan_type"]; got != "pro" {
		t.Fatalf("plan_type = %#v, want stored metadata value %q", got, "pro")
	}
	if got := entry["disabled_reason"]; got != "token revoked" {
		t.Fatalf("disabled_reason = %#v, want %q", got, "token revoked")
	}
}

func TestAuthCodexPlanTypeFallsBackToAttributeAndIgnoresOtherProviders(t *testing.T) {
	codex := &coreauth.Auth{Provider: "codex", Attributes: map[string]string{"plan_type": "plus"}}
	if got := authCodexPlanType(codex); got != "plus" {
		t.Fatalf("authCodexPlanType(codex) = %q, want plus", got)
	}
	claude := &coreauth.Auth{Provider: "claude", Metadata: map[string]any{"plan_type": "max"}}
	if got := authCodexPlanType(claude); got != "" {
		t.Fatalf("authCodexPlanType(claude) = %q, want empty", got)
	}
}
