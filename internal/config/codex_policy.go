package config

import (
	"strings"
)

// Codex instruction modes.
const (
	CodexInstructionsModePrepend = "prepend"
	CodexInstructionsModeAppend  = "append"
	CodexInstructionsModeReplace = "replace"
)

// CodexRoutingStrategyAdaptive is accepted for compatibility with management panels.
// It is stored but has no runtime effect; see CodexRoutingConfig.Strategy.
const CodexRoutingStrategyAdaptive = "adaptive"

// CodexInstructionsConfig configures operator instructions merged into Codex requests.
// When enabled, the instructions are applied to every Codex request whose model matches
// Models, on any eligible credential, before payload rules run.
type CodexInstructionsConfig struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
	// Mode is prepend (default), append, or replace.
	Mode string `yaml:"mode" json:"mode"`
	// Content is the inline instruction text. It wins over File when non-empty.
	Content string `yaml:"content" json:"content"`
	// File is a local path read when Content is empty.
	File string `yaml:"file" json:"file"`
	// Models lists model patterns ("*" wildcards). Empty applies to every Codex model.
	Models []string `yaml:"models" json:"models"`
	// OAuthOnly limits injection to Codex OAuth credentials. Defaults to true.
	OAuthOnly *bool `yaml:"oauth-only,omitempty" json:"oauth-only,omitempty"`
}

// NormalizeCodexInstructionsConfig trims values and canonicalizes the mode.
// It reports false when the mode is not recognised.
func NormalizeCodexInstructionsConfig(value CodexInstructionsConfig) (CodexInstructionsConfig, bool) {
	value.Mode = strings.ToLower(strings.TrimSpace(value.Mode))
	if value.Mode == "" {
		value.Mode = CodexInstructionsModePrepend
	}
	switch value.Mode {
	case CodexInstructionsModePrepend, CodexInstructionsModeAppend, CodexInstructionsModeReplace:
	default:
		return value, false
	}
	value.File = strings.TrimSpace(value.File)
	value.Models = trimNonEmpty(value.Models)
	return value, true
}

// OAuthOnlyEnabled reports whether injection is limited to OAuth credentials.
func (c CodexInstructionsConfig) OAuthOnlyEnabled() bool {
	return c.OAuthOnly == nil || *c.OAuthOnly
}

// MatchesModel reports whether model is eligible. An empty Models list matches every
// model; a provider-prefixed id (e.g. "team/gpt-5.5") also matches bare patterns.
func (c CodexInstructionsConfig) MatchesModel(model string) bool {
	model = strings.TrimSpace(model)
	if len(c.Models) == 0 {
		return true
	}
	if model == "" {
		return false
	}
	for _, pattern := range c.Models {
		if matchGlob(strings.TrimSpace(pattern), model) {
			return true
		}
	}
	if idx := strings.LastIndex(model, "/"); idx >= 0 && idx+1 < len(model) {
		return c.MatchesModel(model[idx+1:])
	}
	return false
}

// matchGlob matches value against a pattern where "*" matches any run of characters.
func matchGlob(pattern, value string) bool {
	if pattern == "" {
		return false
	}
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == value
	}
	if !strings.HasPrefix(value, parts[0]) {
		return false
	}
	pos := len(parts[0])
	for _, part := range parts[1 : len(parts)-1] {
		idx := strings.Index(value[pos:], part)
		if idx < 0 {
			return false
		}
		pos += idx + len(part)
	}
	last := parts[len(parts)-1]
	return len(value)-pos >= len(last) && strings.HasSuffix(value[pos:], last)
}

// CodexRoutingConfig holds Codex-specific credential routing preferences.
type CodexRoutingConfig struct {
	// Strategy is accepted for management-panel compatibility ("" or "adaptive") and
	// persisted, but has no runtime effect: Codex credentials are always selected by the
	// global routing strategy (round-robin, fill-first, weighted, reset-aware) together
	// with session affinity and quota fencing. The panel's "adaptive" mode is a separate
	// concurrency-limiting selector that would replace those mechanisms, so it is not
	// implemented here.
	Strategy string `yaml:"strategy,omitempty" json:"strategy,omitempty"`
	// PreferFreeForSharedModels narrows Codex candidates to Free-plan credentials whenever
	// at least one available Free credential serves the requested model; paid credentials
	// are used only when no Free credential is available. Default false.
	PreferFreeForSharedModels bool `yaml:"prefer-free-for-shared-models,omitempty" json:"prefer-free-for-shared-models,omitempty"`
}

// NormalizeCodexRoutingStrategy canonicalizes the panel strategy value.
func NormalizeCodexRoutingStrategy(strategy string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(strategy)) {
	case "":
		return "", true
	case CodexRoutingStrategyAdaptive:
		return CodexRoutingStrategyAdaptive, true
	default:
		return "", false
	}
}

// CodexFailurePolicy is the effective Codex credential failure policy.
// All knobs default to off so that, unless an operator opts in, the existing
// cooldown, quota-fencing and deactivated-workspace behaviour is unchanged.
type CodexFailurePolicy struct {
	AutoDisableAuthFailures         bool
	AuthFailureDisableAfter         int
	UsageLimitDisableAfter          int
	UsageLimitCooldownFallbackHours int
}

// Defaults used when the failure-policy keys are omitted.
const (
	defaultCodexAuthFailureDisableAfter = 1
)

// FailurePolicy returns the normalized Codex failure policy.
func (c CodexConfig) FailurePolicy() CodexFailurePolicy {
	policy := CodexFailurePolicy{AuthFailureDisableAfter: defaultCodexAuthFailureDisableAfter}
	if c.AutoDisableAuthFailures != nil {
		policy.AutoDisableAuthFailures = *c.AutoDisableAuthFailures
	}
	if c.AuthFailureDisableAfter != nil {
		policy.AuthFailureDisableAfter = max(*c.AuthFailureDisableAfter, 0)
	}
	if c.UsageLimitDisableAfter != nil {
		policy.UsageLimitDisableAfter = max(*c.UsageLimitDisableAfter, 0)
	}
	if c.UsageLimitCooldownFallbackHours != nil {
		policy.UsageLimitCooldownFallbackHours = max(*c.UsageLimitCooldownFallbackHours, 0)
	}
	return policy
}

// AuthFailureThreshold returns how many hard auth failures disable a credential, or 0 when off.
func (p CodexFailurePolicy) AuthFailureThreshold() int {
	if !p.AutoDisableAuthFailures {
		return 0
	}
	return p.AuthFailureDisableAfter
}

func trimNonEmpty(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}
