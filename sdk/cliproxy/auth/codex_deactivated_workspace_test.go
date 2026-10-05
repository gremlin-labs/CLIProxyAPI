package auth

import (
	"context"
	"net/http"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

func markCodexFailure(t *testing.T, provider string, failure *Error) *Auth {
	t.Helper()
	manager := NewManager(nil, nil, nil)
	auth := &Auth{ID: t.Name(), Provider: provider, Status: StatusActive}
	if _, errRegister := manager.Register(context.Background(), auth); errRegister != nil {
		t.Fatalf("Register() error = %v", errRegister)
	}
	manager.MarkResult(context.Background(), Result{
		AuthID:   auth.ID,
		Provider: provider,
		Model:    "gpt-5",
		Error:    failure,
	})
	updated, ok := manager.GetByID(auth.ID)
	if !ok || updated == nil {
		t.Fatal("auth missing after MarkResult")
	}
	return updated
}

func TestMarkResultDisablesCodexDeactivatedWorkspace(t *testing.T) {
	for name, failure := range map[string]*Error{
		"402 body": {HTTPStatus: http.StatusPaymentRequired, Message: `{"detail":{"code":"deactivated_workspace"}}`},
		"code":     {HTTPStatus: http.StatusPaymentRequired, Code: "deactivated_workspace", Message: "payment required"},
	} {
		t.Run(name, func(t *testing.T) {
			updated := markCodexFailure(t, "codex", failure)
			if !updated.Disabled || updated.Status != StatusDisabled {
				t.Fatalf("Disabled=%t Status=%q, want a disabled credential", updated.Disabled, updated.Status)
			}
			if disabled, _ := updated.Metadata["disabled"].(bool); !disabled {
				t.Fatal(`metadata "disabled" not set; the state would be lost on restart`)
			}
			if updated.StatusMessage != codexDeactivatedWorkspaceStatus {
				t.Fatalf("StatusMessage = %q, want %q", updated.StatusMessage, codexDeactivatedWorkspaceStatus)
			}
		})
	}
}

func TestMarkResultKeepsCooldownForOtherPaymentFailures(t *testing.T) {
	cases := map[string]struct {
		provider string
		failure  *Error
	}{
		"codex plain 402":          {"codex", &Error{HTTPStatus: http.StatusPaymentRequired, Message: "billing hard limit reached"}},
		"claude deactivated body":  {"claude", &Error{HTTPStatus: http.StatusPaymentRequired, Message: "deactivated_workspace"}},
		"codex unrelated 403 body": {"codex", &Error{HTTPStatus: http.StatusForbidden, Message: "forbidden"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			updated := markCodexFailure(t, tc.provider, tc.failure)
			if updated.Disabled || updated.Status == StatusDisabled {
				t.Fatalf("credential disabled for %s; want the ordinary cooldown", name)
			}
		})
	}
}

func TestDeactivatedWorkspaceLeavesSelectionUntilReEnabled(t *testing.T) {
	ctx := context.Background()
	auth := &Auth{ID: "codex-deactivated-select", Provider: "codex", Status: StatusActive}
	registry.GetGlobalRegistry().RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: "gpt-5"}})
	t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(auth.ID) })

	manager := NewManager(nil, nil, nil)
	manager.RegisterExecutor(&refreshMockExecutor{id: "codex"})
	if _, errRegister := manager.Register(ctx, auth); errRegister != nil {
		t.Fatalf("Register() error = %v", errRegister)
	}
	if picked, _, errPick := manager.pickNext(ctx, "codex", "gpt-5", cliproxyexecutor.Options{}, nil); errPick != nil || picked == nil {
		t.Fatalf("pickNext() before failure = %v, %v; want the credential", picked, errPick)
	}

	manager.MarkResult(ctx, Result{
		AuthID:   auth.ID,
		Provider: "codex",
		Model:    "gpt-5",
		Error:    &Error{HTTPStatus: http.StatusPaymentRequired, Message: `{"detail":{"code":"deactivated_workspace"}}`},
	})
	if picked, _, _ := manager.pickNext(ctx, "codex", "gpt-5", cliproxyexecutor.Options{}, nil); picked != nil {
		t.Fatalf("pickNext() selected deactivated credential %s", picked.ID)
	}

	// Re-enable the way the management toggle does.
	current, _ := manager.GetByID(auth.ID)
	reEnabled := current.Clone()
	reEnabled.Disabled = false
	reEnabled.Status = StatusActive
	reEnabled.StatusMessage = ""
	reEnabled.Metadata["disabled"] = false
	if _, errUpdate := manager.Update(ctx, reEnabled); errUpdate != nil {
		t.Fatalf("Update() error = %v", errUpdate)
	}
	if picked, _, errPick := manager.pickNext(ctx, "codex", "gpt-5", cliproxyexecutor.Options{}, nil); errPick != nil || picked == nil {
		t.Fatalf("pickNext() after re-enable = %v, %v; want the credential back", picked, errPick)
	}
}
