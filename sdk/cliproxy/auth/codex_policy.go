package auth

// Codex Config page runtime: private-instruction credential routing, the opt-in
// Free-plan preference, and the opt-in Codex credential failure policy. These hook
// into the existing selection and cooldown flow; they do not replace any selector.

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/codexinstructions"
	internalconfig "github.com/router-for-me/CLIProxyAPI/v8/internal/config"
)

func isCodexCredential(auth *Auth) bool {
	return auth != nil && strings.EqualFold(strings.TrimSpace(auth.Provider), "codex")
}

func (m *Manager) loadRuntimeConfig() *internalconfig.Config {
	if m == nil {
		return nil
	}
	cfg, _ := m.runtimeConfig.Load().(*internalconfig.Config)
	return cfg
}

// CodexInstructionsConfig returns a copy of the runtime private-instruction settings,
// or nil when no runtime config is loaded.
func (m *Manager) CodexInstructionsConfig() *internalconfig.CodexInstructionsConfig {
	cfg := m.loadRuntimeConfig()
	if cfg == nil {
		return nil
	}
	copyCfg := cfg.Codex.Instructions
	return &copyCfg
}

// codexPreferFreeEnabled reports whether Codex routing prefers Free-plan credentials.
func (m *Manager) codexPreferFreeEnabled() bool {
	cfg := m.loadRuntimeConfig()
	return cfg != nil && cfg.Codex.Routing.PreferFreeForSharedModels
}

// preferFreeCodexBuckets narrows each priority bucket to Free-plan Codex credentials and
// non-Codex credentials when at least one available Free-plan Codex credential exists.
// Paid Codex credentials stay eligible whenever no Free credential is available.
func preferFreeCodexBuckets(buckets map[int][]*Auth) map[int][]*Auth {
	hasFree := false
	for _, auths := range buckets {
		for _, candidate := range auths {
			if isFreeCodexAuth(candidate) {
				hasFree = true
				break
			}
		}
		if hasFree {
			break
		}
	}
	if !hasFree {
		return buckets
	}
	out := make(map[int][]*Auth, len(buckets))
	for priority, auths := range buckets {
		kept := make([]*Auth, 0, len(auths))
		for _, candidate := range auths {
			if !isCodexCredential(candidate) || isFreeCodexAuth(candidate) {
				kept = append(kept, candidate)
			}
		}
		if len(kept) > 0 {
			out[priority] = kept
		}
	}
	return out
}

// codexFailureKind identifies Codex failures counted by the failure policy.
type codexFailureKind int

const (
	codexFailureNone codexFailureKind = iota
	codexFailureUsageLimit
	codexFailureAuth
)

func codexFailureKindForResult(result Result) codexFailureKind {
	if result.Success || result.Error == nil || !strings.EqualFold(strings.TrimSpace(result.Provider), "codex") {
		return codexFailureNone
	}
	if isCodexUsageLimitResultError(result.Error) {
		return codexFailureUsageLimit
	}
	if isCodexHardAuthFailure(result.Error) {
		return codexFailureAuth
	}
	return codexFailureNone
}

func isCodexUsageLimitResultError(err *Error) bool {
	if err == nil {
		return false
	}
	status := err.StatusCode()
	if status != 0 && status != http.StatusTooManyRequests && status != http.StatusBadRequest {
		return false
	}
	body := strings.ToLower(err.Code + " " + err.Message)
	return strings.Contains(body, "usage_limit_reached") || strings.Contains(body, "hit your usage limit")
}

// isCodexHardAuthFailure reports failures that a cooldown or token refresh does not fix.
// A deactivated workspace is excluded because it is already disabled unconditionally,
// and other 402s keep the ordinary cooldown.
func isCodexHardAuthFailure(err *Error) bool {
	if err == nil || isCodexDeactivatedWorkspace("codex", err) {
		return false
	}
	if isInvalidGrantResultError(err) {
		return true
	}
	body := strings.ToLower(err.Code + " " + err.Message)
	for _, marker := range []string{"refresh_token_reused", "token has been expired or revoked", "access token invalidated"} {
		if strings.Contains(body, marker) {
			return true
		}
	}
	if err.StatusCode() != http.StatusUnauthorized {
		return false
	}
	// The conductor retries a 401 once after refreshing the token, so a 401 that reaches
	// here survived the refresh. Request-scoped and model-support 401s are not auth death.
	return !isRequestScopedNotFoundResultError(err) && !isModelSupportResultError(err)
}

// applyCodexUsageLimitFallback supplies the configured cooldown for a Codex
// usage_limit_reached rejection that carries no reset time.
func (m *Manager) applyCodexUsageLimitFallback(result Result) Result {
	if result.RetryAfter != nil || codexFailureKindForResult(result) != codexFailureUsageLimit {
		return result
	}
	cfg := m.loadRuntimeConfig()
	if cfg == nil {
		return result
	}
	hours := cfg.Codex.FailurePolicy().UsageLimitCooldownFallbackHours
	if hours <= 0 {
		return result
	}
	fallback := time.Duration(hours) * time.Hour
	result.RetryAfter = &fallback
	return result
}

// trackCodexFailure updates the consecutive failure counters of a Codex credential and
// reports the reason when the configured threshold disables it. Counters are runtime
// state: a success resets them and they are not persisted. A failure observed while
// the credential is still cooling down for the same model is not counted again.
// The caller must hold m.mu and call this before applying the failure's cooldown.
func (m *Manager) trackCodexFailure(auth *Auth, result Result, modelKey string, now time.Time) (string, bool) {
	if auth == nil {
		return "", false
	}
	if result.Success {
		auth.codexUsageLimitHits = 0
		auth.codexAuthFailureHits = 0
		return "", false
	}
	kind := codexFailureKindForResult(result)
	if kind == codexFailureNone {
		return "", false
	}
	cfg := m.loadRuntimeConfig()
	if cfg == nil {
		return "", false
	}
	policy := cfg.Codex.FailurePolicy()
	switch kind {
	case codexFailureUsageLimit:
		threshold := policy.UsageLimitDisableAfter
		if threshold <= 0 || codexCoolingForModel(auth, modelKey, now) {
			return "", false
		}
		auth.codexUsageLimitHits++
		if auth.codexUsageLimitHits >= threshold {
			return fmt.Sprintf("disabled (codex usage limit reached %d times in a row)", auth.codexUsageLimitHits), true
		}
	case codexFailureAuth:
		threshold := policy.AuthFailureThreshold()
		if threshold <= 0 {
			return "", false
		}
		auth.codexAuthFailureHits++
		if auth.codexAuthFailureHits >= threshold {
			return fmt.Sprintf("disabled (codex auth failure %d times in a row)", auth.codexAuthFailureHits), true
		}
	}
	return "", false
}

func codexCoolingForModel(auth *Auth, modelKey string, now time.Time) bool {
	if auth == nil {
		return false
	}
	if auth.Unavailable && auth.NextRetryAfter.After(now) {
		return true
	}
	if state := existingModelState(auth, modelKey); state != nil {
		return state.Unavailable && state.NextRetryAfter.After(now)
	}
	return false
}

// privateInstructionsPolicy carries the private-instruction routing flags the request
// handler resolved into execution metadata.
type privateInstructionsPolicy struct {
	active bool
	meta   map[string]any
}

func privateInstructionsPolicyFromMetadata(meta map[string]any) privateInstructionsPolicy {
	if !codexinstructions.RequestIsPrivate(meta) && !codexinstructions.RequestReservesMarkedAuths(meta) {
		return privateInstructionsPolicy{}
	}
	return privateInstructionsPolicy{active: true, meta: meta}
}

func (p privateInstructionsPolicy) allows(auth *Auth) bool {
	if !p.active {
		return true
	}
	return codexinstructions.AuthMatchesPolicy(isCodexCredential(auth), auth.Attributes, auth.Metadata, p.meta)
}
