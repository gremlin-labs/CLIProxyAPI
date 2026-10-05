package handlers

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/codexinstructions"
	internalconfig "github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
)

func privateInstructionsManager(settings internalconfig.CodexInstructionsConfig) *coreauth.Manager {
	manager := coreauth.NewManager(nil, nil, nil)
	manager.SetConfig(&internalconfig.Config{Codex: internalconfig.CodexConfig{Instructions: settings}})
	return manager
}

func TestApplyPrivateCodexInstructionModel(t *testing.T) {
	manager := privateInstructionsManager(internalconfig.CodexInstructionsConfig{Enabled: true})

	meta := map[string]any{}
	if got := applyPrivateCodexInstructionModel(manager, "private/gpt-5.5(high)", meta); got != "gpt-5.5(high)" {
		t.Fatalf("model = %q, want marker stripped and suffix kept", got)
	}
	if !codexinstructions.RequestIsPrivate(meta) || !codexinstructions.RequestRequiresAuthAllow(meta) {
		t.Fatalf("metadata = %v, want private request requiring marked auths", meta)
	}

	meta = map[string]any{}
	if got := applyPrivateCodexInstructionModel(manager, "gpt-5.5", meta); got != "gpt-5.5" {
		t.Fatalf("model = %q, want unchanged", got)
	}
	if len(meta) != 0 {
		t.Fatalf("metadata = %v, want empty for a normal request", meta)
	}
}

func TestApplyPrivateCodexInstructionModelDisabledLeavesModel(t *testing.T) {
	manager := privateInstructionsManager(internalconfig.CodexInstructionsConfig{Enabled: false})
	meta := map[string]any{}
	if got := applyPrivateCodexInstructionModel(manager, "private/gpt-5.5", meta); got != "private/gpt-5.5" {
		t.Fatalf("model = %q, want unchanged when disabled", got)
	}
	if len(meta) != 0 {
		t.Fatalf("metadata = %v, want empty when disabled", meta)
	}
}

func TestApplyPrivateCodexInstructionModelReserveAndNoMarkers(t *testing.T) {
	usePrefix := false
	manager := privateInstructionsManager(internalconfig.CodexInstructionsConfig{
		Enabled:            true,
		ReserveMarkedAuths: true,
		UsePrefixSuffix:    &usePrefix,
		Models:             []string{"gpt-5*"},
	})

	meta := map[string]any{}
	if got := applyPrivateCodexInstructionModel(manager, "gpt-5.5", meta); got != "gpt-5.5" {
		t.Fatalf("model = %q, want unchanged without markers", got)
	}
	if !codexinstructions.RequestIsPrivate(meta) {
		t.Fatalf("metadata = %v, want eligible model private without markers", meta)
	}

	meta = map[string]any{}
	applyPrivateCodexInstructionModel(manager, "o3", meta)
	if codexinstructions.RequestIsPrivate(meta) || !codexinstructions.RequestReservesMarkedAuths(meta) {
		t.Fatalf("metadata = %v, want normal request that skips marked auths", meta)
	}
}

func TestGetRequestDetailsResolvesPrivateCodexModel(t *testing.T) {
	modelRegistry := registry.GetGlobalRegistry()
	modelRegistry.RegisterClient("test-private-codex-model", "codex", []*registry.ModelInfo{{ID: "gpt-5.5-private-test"}})
	t.Cleanup(func() { modelRegistry.UnregisterClient("test-private-codex-model") })

	manager := privateInstructionsManager(internalconfig.CodexInstructionsConfig{Enabled: true})
	handler := NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, manager)
	providers, normalized, errMsg := handler.getRequestDetails("private/gpt-5.5-private-test(high)")
	if errMsg != nil {
		t.Fatalf("getRequestDetails error = %v", errMsg.Error)
	}
	if len(providers) != 1 || providers[0] != "codex" {
		t.Fatalf("providers = %v, want [codex]", providers)
	}
	if normalized != "private/gpt-5.5-private-test(high)" {
		t.Fatalf("normalized = %q, want marker kept for metadata resolution", normalized)
	}

	disabled := NewBaseAPIHandlers(&sdkconfig.SDKConfig{}, privateInstructionsManager(internalconfig.CodexInstructionsConfig{}))
	if _, _, errMsg = disabled.getRequestDetails("private/gpt-5.5-private-test"); errMsg == nil {
		t.Fatal("getRequestDetails resolved a private marker while private instructions are disabled")
	}
}

func TestExpandPrivateCodexInstructionModels(t *testing.T) {
	manager := privateInstructionsManager(internalconfig.CodexInstructionsConfig{
		Enabled: true,
		Models:  []string{"gpt-5*"},
		RequestMarkers: internalconfig.CodexInstructionMarkersConfig{
			Prefixes: []string{"private/"},
			Suffixes: []string{"-private"},
		},
	})
	models := []map[string]any{
		{"id": "gpt-5.5", "display_name": "gpt-5.5", "capabilities": []string{"tools"}},
		{"id": "o3"},
	}
	got := ExpandPrivateCodexInstructionModels(manager, models, "id")
	ids := make([]string, 0, len(got))
	for _, model := range got {
		ids = append(ids, model["id"].(string))
	}
	want := []string{"gpt-5.5", "private/gpt-5.5", "gpt-5.5-private", "o3"}
	if len(ids) != len(want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("ids = %v, want %v", ids, want)
		}
	}
	if got[1]["display_name"] != "private/gpt-5.5" {
		t.Fatalf("display_name = %v, want virtual id", got[1]["display_name"])
	}
	got[1]["capabilities"].([]string)[0] = "mutated"
	if models[0]["capabilities"].([]string)[0] != "tools" {
		t.Fatal("expanded model shares nested values with the source model")
	}

	disabled := privateInstructionsManager(internalconfig.CodexInstructionsConfig{})
	if got := ExpandPrivateCodexInstructionModels(disabled, models, "id"); len(got) != len(models) {
		t.Fatalf("disabled expansion = %d models, want %d", len(got), len(models))
	}
}
