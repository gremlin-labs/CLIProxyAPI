package handlers

// Private Codex instructions: the handler resolves private mode from the requested
// model id, strips the marker, and records the routing flags in execution metadata.
// Auth selection and the Codex executors act on that metadata.

import (
	"fmt"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/codexinstructions"
	internalconfig "github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v8/internal/thinking"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
)

func codexInstructionsSettings(manager *coreauth.Manager) *internalconfig.CodexInstructionsConfig {
	if manager == nil {
		return nil
	}
	settings := manager.CodexInstructionsConfig()
	if settings == nil || !settings.Enabled {
		return nil
	}
	return settings
}

// parsePrivateCodexModel reports the model without its private marker and whether the
// request runs in private mode. Thinking suffixes such as "(high)" are preserved.
func parsePrivateCodexModel(settings *internalconfig.CodexInstructionsConfig, modelName string) (string, bool) {
	modelName = strings.TrimSpace(modelName)
	if settings == nil || modelName == "" {
		return modelName, false
	}
	parsed := thinking.ParseSuffix(modelName)
	base := strings.TrimSpace(parsed.ModelName)
	if !settings.UsePrefixSuffixEnabled() {
		// Without markers every eligible request is private and model ids are unchanged.
		return modelName, codexinstructions.ModelMatches(settings.Models, base)
	}
	stripped, private := codexinstructions.ParseModel(base, settings.Markers())
	if !private {
		return modelName, false
	}
	if parsed.HasSuffix {
		return fmt.Sprintf("%s(%s)", stripped, parsed.RawSuffix), true
	}
	return stripped, true
}

// stripPrivateCodexMarker removes a private marker so registry provider lookup finds
// the real model (e.g. private/gpt-5.5 resolves like gpt-5.5).
func stripPrivateCodexMarker(manager *coreauth.Manager, modelName string) string {
	stripped, _ := parsePrivateCodexModel(codexInstructionsSettings(manager), modelName)
	return stripped
}

// applyPrivateCodexInstructionModel strips the private marker from modelName and
// records the private-instruction routing flags in meta. When marked credentials are
// reserved, normal requests are flagged so selection keeps them off marked credentials.
func applyPrivateCodexInstructionModel(manager *coreauth.Manager, modelName string, meta map[string]any) string {
	settings := codexInstructionsSettings(manager)
	if settings == nil || meta == nil {
		return modelName
	}
	stripped, private := parsePrivateCodexModel(settings, modelName)
	if !private {
		if settings.ReserveMarkedAuths && !codexinstructions.RequestIsPrivate(meta) {
			meta[codexinstructions.RequestReserveMarkedAuthsMetadataKey] = true
		}
		return modelName
	}
	delete(meta, codexinstructions.RequestReserveMarkedAuthsMetadataKey)
	meta[codexinstructions.RequestPrivateMetadataKey] = true
	meta[codexinstructions.RequestRequireAuthAllowMetadataKey] = settings.RequireAuthAllowEnabled()
	return stripped
}

// ExpandPrivateCodexInstructionModels adds private marker variants (for example
// "private/gpt-5.5") of eligible models to a model listing. Entries are matched and
// rewritten by idKey ("id" for /v1/models, "slug" for the Codex client catalog).
func ExpandPrivateCodexInstructionModels(manager *coreauth.Manager, models []map[string]any, idKey string) []map[string]any {
	settings := codexInstructionsSettings(manager)
	if settings == nil || !settings.UsePrefixSuffixEnabled() {
		return models
	}
	markers := settings.Markers()
	if len(markers.Prefixes) == 0 && len(markers.Suffixes) == 0 {
		return models
	}
	out := make([]map[string]any, 0, len(models)*2)
	seen := make(map[string]struct{}, len(models)*2)
	for _, model := range models {
		if model == nil {
			continue
		}
		id, _ := model[idKey].(string)
		id = strings.TrimSpace(id)
		if id == "" {
			out = append(out, model)
			continue
		}
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			out = append(out, model)
		}
		if !codexinstructions.ModelMatches(settings.Models, id) {
			continue
		}
		for _, virtualID := range codexinstructions.VirtualModelIDs(id, markers) {
			if _, ok := seen[virtualID]; ok {
				continue
			}
			seen[virtualID] = struct{}{}
			clone := cloneModelMap(model)
			for _, key := range []string{idKey, "id", "display_name"} {
				if _, ok := clone[key]; ok {
					clone[key] = virtualID
				}
			}
			out = append(out, clone)
		}
	}
	return out
}

func cloneModelMap(model map[string]any) map[string]any {
	out := make(map[string]any, len(model))
	for key, value := range model {
		out[key] = cloneModelMapValue(value)
	}
	return out
}

func cloneModelMapValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneModelMap(typed)
	case []any:
		out := make([]any, len(typed))
		for i, entry := range typed {
			out[i] = cloneModelMapValue(entry)
		}
		return out
	case []string:
		return append([]string(nil), typed...)
	default:
		return value
	}
}
