package openai

import (
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
)

// openAIModelCapabilityMetadata returns optional capability fields for one /v1/models entry:
// context_window / max_context_window and supported_reasoning_levels /
// default_reasoning_level. Fields are omitted when the registry has no data, so clients
// never receive guessed values.
func openAIModelCapabilityMetadata(model map[string]any) map[string]any {
	metadata := make(map[string]any)
	modelID, _ := model["id"].(string)
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return metadata
	}
	info := registry.LookupModelInfo(modelID)

	// An operator model-context override wins, then a configured max_context_length,
	// then the catalog context length.
	contextWindow := 0
	if override, ok := registry.LookupModelContextOverride(modelID); ok {
		contextWindow = override.ContextLength
	}
	if contextWindow == 0 {
		contextWindow = positiveInt(model["max_context_length"])
	}
	if contextWindow == 0 {
		contextWindow = positiveInt(model["context_length"])
	}
	if contextWindow == 0 && info != nil {
		if info.MaxContextLength > 0 {
			contextWindow = info.MaxContextLength
		} else {
			contextWindow = info.ContextLength
		}
	}
	if contextWindow > 0 {
		metadata["context_window"] = contextWindow
		metadata["max_context_window"] = contextWindow
	}

	if info != nil && info.Thinking != nil {
		if levels, defaultLevel := reasoningLevels(info.Thinking.Levels); len(levels) > 0 {
			metadata["supported_reasoning_levels"] = levels
			metadata["default_reasoning_level"] = defaultLevel
		}
	}
	return metadata
}

// reasoningLevels normalizes declared thinking levels and picks a default: medium when
// offered, otherwise the first level other than none.
func reasoningLevels(raw []string) ([]string, string) {
	levels := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	defaultLevel := ""
	for _, rawLevel := range raw {
		level := strings.ToLower(strings.TrimSpace(rawLevel))
		if level == "" {
			continue
		}
		if _, dup := seen[level]; dup {
			continue
		}
		seen[level] = struct{}{}
		levels = append(levels, level)
		if (defaultLevel == "" && level != "none") || level == "medium" {
			defaultLevel = level
		}
	}
	if len(levels) == 0 {
		return nil, ""
	}
	if defaultLevel == "" {
		defaultLevel = levels[0]
	}
	return levels, defaultLevel
}

func positiveInt(value any) int {
	switch v := value.(type) {
	case int:
		if v > 0 {
			return v
		}
	case int64:
		if v > 0 {
			return int(v)
		}
	case float64:
		if v > 0 {
			return int(v)
		}
	}
	return 0
}
