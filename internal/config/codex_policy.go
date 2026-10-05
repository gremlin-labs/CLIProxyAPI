package config

import (
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/codexinstructions"
)

// Codex private-instruction modes.
const (
	CodexInstructionsModePrepend = "prepend"
	CodexInstructionsModeAppend  = "append"
	CodexInstructionsModeReplace = "replace"
)

// CodexRoutingStrategyAdaptive is accepted for compatibility with management panels.
// It is stored but has no runtime effect; see CodexRoutingConfig.Strategy.
const CodexRoutingStrategyAdaptive = "adaptive"

// CodexInstructionsConfig configures private Codex instruction injection.
//
// When enabled, requests whose model id carries a configured marker (default prefix
// "private/"), or every eligible request when UsePrefixSuffix is false, run in
// private mode: the marker is stripped, the request is routed only to Codex
// credentials allowed for private instructions, and the configured instructions are
// merged into the request's top-level instructions before payload rules apply.
type CodexInstructionsConfig struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
	// Mode is prepend (default), append, or replace.
	Mode string `yaml:"mode" json:"mode"`
	// Content is the inline instruction text. It wins over File when non-empty.
	Content string `yaml:"content" json:"content"`
	// File is a local path read when Content is empty.
	File string `yaml:"file" json:"file"`
	// Models lists eligible model patterns ("*" wildcards). Empty means gpt-5.5 and gpt-5*.
	Models []string `yaml:"models" json:"models"`
	// OAuthOnly limits injection to Codex OAuth credentials. Defaults to true.
	OAuthOnly *bool `yaml:"oauth-only,omitempty" json:"oauth-only,omitempty"`
	// RequireAuthAllow restricts private requests to credentials marked
	// allow_private_instructions. Defaults to true.
	RequireAuthAllow *bool `yaml:"require-auth-allow,omitempty" json:"require-auth-allow,omitempty"`
	// ReserveMarkedAuths keeps marked credentials out of normal (non-private) traffic.
	ReserveMarkedAuths bool `yaml:"reserve-marked-auths,omitempty" json:"reserve-marked-auths,omitempty"`
	// UsePrefixSuffix keeps private mode opt-in through RequestMarkers. Defaults to true.
	// When false, every request for an eligible model uses private mode.
	UsePrefixSuffix *bool `yaml:"use-prefix-suffix,omitempty" json:"use-prefix-suffix,omitempty"`
	// RequestMarkers lists model-id prefixes/suffixes that select private mode. When both
	// lists are omitted the "private/" prefix is used; an explicitly empty list disables
	// that marker type.
	RequestMarkers CodexInstructionMarkersConfig `yaml:"request-markers,omitempty" json:"request-markers,omitempty"`
}

// CodexInstructionMarkersConfig lists private-mode model-id markers.
type CodexInstructionMarkersConfig struct {
	Prefixes []string `yaml:"prefixes" json:"prefixes"`
	Suffixes []string `yaml:"suffixes" json:"suffixes"`
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
	if value.RequestMarkers.Prefixes != nil {
		value.RequestMarkers.Prefixes = trimNonEmptyKeepEmpty(value.RequestMarkers.Prefixes)
	}
	if value.RequestMarkers.Suffixes != nil {
		value.RequestMarkers.Suffixes = trimNonEmptyKeepEmpty(value.RequestMarkers.Suffixes)
	}
	return value, true
}

// OAuthOnlyEnabled reports whether injection is limited to OAuth credentials.
func (c CodexInstructionsConfig) OAuthOnlyEnabled() bool {
	return c.OAuthOnly == nil || *c.OAuthOnly
}

// RequireAuthAllowEnabled reports whether private requests need marked credentials.
func (c CodexInstructionsConfig) RequireAuthAllowEnabled() bool {
	return c.RequireAuthAllow == nil || *c.RequireAuthAllow
}

// UsePrefixSuffixEnabled reports whether private mode is selected by model markers.
func (c CodexInstructionsConfig) UsePrefixSuffixEnabled() bool {
	return c.UsePrefixSuffix == nil || *c.UsePrefixSuffix
}

// Markers returns the normalized marker configuration.
func (c CodexInstructionsConfig) Markers() codexinstructions.MarkerConfig {
	return codexinstructions.NormalizeMarkers(codexinstructions.MarkerConfig{
		Prefixes: c.RequestMarkers.Prefixes,
		Suffixes: c.RequestMarkers.Suffixes,
	})
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
	return trimNonEmptyKeepEmpty(values)
}

func trimNonEmptyKeepEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}
