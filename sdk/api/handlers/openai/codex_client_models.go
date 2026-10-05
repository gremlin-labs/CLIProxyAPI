package openai

import (
	codexmodels "github.com/router-for-me/CLIProxyAPI/v8/internal/client/codex/models"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers"
)

func (h *OpenAIAPIHandler) codexClientModelsResponse(clientVersion ...string) map[string]any {
	version := ""
	if len(clientVersion) > 0 {
		version = clientVersion[0]
	}
	optimizeMultiAgentV2 := h != nil && h.Cfg != nil && h.Cfg.Client.Codex.OptimizeMultiAgentV2
	var applyPatchCapabilityForModel codexmodels.ApplyPatchCapabilityForModelFunc
	if h != nil && h.Cfg != nil && h.Cfg.Client.Codex.EnableApplyPatch {
		applyPatchCapabilityForModel = h.SupportsApplyPatchModel
	}
	modelRegistry := registry.GetGlobalRegistry()
	// Build from the registry models, then clone catalog entries for private-instruction
	// marker ids so the variants keep the full template and capability metadata.
	built := codexmodels.BuildResponseForClientWithToolCapabilities(modelRegistry.GetAvailableModels("openai"), modelRegistry.GetModelProviders, modelRegistry.GetResponsesWebSearchCapability, applyPatchCapabilityForModel, optimizeMultiAgentV2, version)
	if h != nil && h.BaseAPIHandler != nil {
		if models, ok := built["models"].([]map[string]any); ok {
			built["models"] = handlers.ExpandPrivateCodexInstructionModels(h.AuthManager, models, "slug")
		}
	}
	return built
}

// CodexClientModelsResponse builds a Codex client model response.
func CodexClientModelsResponse(models []map[string]any) map[string]any {
	return codexmodels.BuildResponse(models, nil, false)
}

// CodexClientModelsResponseWithMultiAgentV2 builds a Codex client model response
// and advertises multi-agent v2 for synthesized models when enabled.
func CodexClientModelsResponseWithMultiAgentV2(models []map[string]any, enabled bool) map[string]any {
	return codexmodels.BuildResponse(models, nil, enabled)
}

// CodexClientModelsResponseForClient builds a Codex client model response
// tailored for a specific client version.
func CodexClientModelsResponseForClient(models []map[string]any, clientVersion string, enabled bool) map[string]any {
	return codexmodels.BuildResponseForClient(models, nil, enabled, clientVersion)
}
