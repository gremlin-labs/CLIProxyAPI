package auth

import (
	"context"
	"net/http"
	"testing"
	"time"

	internalconfig "github.com/router-for-me/CLIProxyAPI/v8/internal/config"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

func newCodexPolicyTestManager(t *testing.T, selector Selector, cfg *internalconfig.Config, model string, auths ...*Auth) *Manager {
	t.Helper()
	ids := make([]string, 0, len(auths))
	for _, candidate := range auths {
		ids = append(ids, candidate.ID)
	}
	registerSchedulerModels(t, "codex", model, ids...)
	manager := NewManager(nil, selector, nil)
	manager.executors["codex"] = schedulerTestExecutor{}
	manager.SetConfig(cfg)
	for _, candidate := range auths {
		if _, errRegister := manager.Register(context.Background(), candidate); errRegister != nil {
			t.Fatalf("Register(%s) error = %v", candidate.ID, errRegister)
		}
	}
	return manager
}

func preferFreeConfig() *internalconfig.Config {
	return &internalconfig.Config{Codex: internalconfig.CodexConfig{
		Routing: internalconfig.CodexRoutingConfig{PreferFreeForSharedModels: true},
	}}
}

func TestCodexPreferFreeForSharedModels(t *testing.T) {
	selectors := []struct {
		name     string
		selector func() Selector
	}{
		{"round-robin", func() Selector { return &RoundRobinSelector{} }},
		{"weighted-round-robin", func() Selector { return &WeightedRoundRobinSelector{} }},
		{"fill-first", func() Selector { return &FillFirstSelector{} }},
		{"reset-aware", func() Selector { return &ResetAwareSelector{} }},
		{"session-affinity", func() Selector { return NewSessionAffinitySelector(&RoundRobinSelector{}) }},
	}
	for _, tt := range selectors {
		t.Run(tt.name, func(t *testing.T) {
			model := "codex-prefer-free-" + tt.name
			freeID := "free-" + tt.name
			paidID := "paid-" + tt.name
			manager := newCodexPolicyTestManager(t, tt.selector(), preferFreeConfig(), model,
				&Auth{ID: freeID, Provider: "codex", Attributes: map[string]string{"plan_type": "free", "priority": "0", AttributeWeight: "1"}},
				&Auth{ID: paidID, Provider: "codex", Attributes: map[string]string{"plan_type": "pro", "priority": "100", AttributeWeight: "100"}},
			)

			selected, _, errPick := manager.pickNext(context.Background(), "codex", model, cliproxyexecutor.Options{}, nil)
			if errPick != nil {
				t.Fatalf("pickNext() error = %v", errPick)
			}
			if selected == nil || selected.ID != freeID {
				t.Fatalf("pickNext() auth = %v, want %q", selected, freeID)
			}

			selected, _, errPick = manager.pickNext(context.Background(), "codex", model, cliproxyexecutor.Options{}, map[string]struct{}{freeID: {}})
			if errPick != nil {
				t.Fatalf("pickNext() fallback error = %v", errPick)
			}
			if selected == nil || selected.ID != paidID {
				t.Fatalf("pickNext() fallback auth = %v, want %q", selected, paidID)
			}
		})
	}
}

func TestCodexPreferFreeFallsBackWhenFreeCooling(t *testing.T) {
	model := "codex-prefer-free-cooling"
	manager := newCodexPolicyTestManager(t, &RoundRobinSelector{}, preferFreeConfig(), model,
		&Auth{ID: "cooling-free", Provider: "codex", Attributes: map[string]string{"plan_type": "free"},
			Unavailable: true, NextRetryAfter: time.Now().Add(time.Hour)},
		&Auth{ID: "cooling-paid", Provider: "codex", Attributes: map[string]string{"plan_type": "plus"}},
	)
	selected, _, errPick := manager.pickNext(context.Background(), "codex", model, cliproxyexecutor.Options{}, nil)
	if errPick != nil {
		t.Fatalf("pickNext() error = %v", errPick)
	}
	if selected == nil || selected.ID != "cooling-paid" {
		t.Fatalf("pickNext() auth = %v, want cooling-paid", selected)
	}
}

func TestCodexPreferFreeDefaultsOff(t *testing.T) {
	model := "codex-prefer-free-off"
	manager := newCodexPolicyTestManager(t, &FillFirstSelector{}, &internalconfig.Config{}, model,
		&Auth{ID: "off-free", Provider: "codex", Attributes: map[string]string{"plan_type": "free", "priority": "0"}},
		&Auth{ID: "off-paid", Provider: "codex", Attributes: map[string]string{"plan_type": "plus", "priority": "10"}},
	)
	selected, _, errPick := manager.pickNext(context.Background(), "codex", model, cliproxyexecutor.Options{}, nil)
	if errPick != nil {
		t.Fatalf("pickNext() error = %v", errPick)
	}
	if selected == nil || selected.ID != "off-paid" {
		t.Fatalf("pickNext() auth = %v, want priority winner off-paid", selected)
	}
}

func codexFailurePolicyConfig(policy func(*internalconfig.CodexConfig)) *internalconfig.Config {
	cfg := &internalconfig.Config{}
	policy(&cfg.Codex)
	return cfg
}

func intPtr(v int) *int    { return &v }
func boolPtr(v bool) *bool { return &v }

func codexUsageLimitResult(authID, model string) Result {
	return Result{AuthID: authID, Provider: "codex", Model: model, Error: &Error{
		HTTPStatus: http.StatusTooManyRequests,
		Message:    `{"error":{"type":"usage_limit_reached","message":"The usage limit has been reached"}}`,
	}}
}

// expireCodexCooldown moves every cooldown of the auth into the past so the next
// failure is observed after the cooldown window.
func expireCodexCooldown(manager *Manager, authID string) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	auth := manager.auths[authID]
	past := time.Now().Add(-time.Minute)
	auth.NextRetryAfter = past
	for _, state := range auth.ModelStates {
		state.NextRetryAfter = past
		state.Quota.NextRecoverAt = past
	}
}

func codexAuthDisabled(manager *Manager, authID string) bool {
	auth, ok := manager.GetByID(authID)
	return ok && auth.Disabled && auth.Status == StatusDisabled
}

func TestCodexUsageLimitDisableAfterThreshold(t *testing.T) {
	model := "codex-usage-limit-disable"
	authID := "usage-limit-auth"
	cfg := codexFailurePolicyConfig(func(c *internalconfig.CodexConfig) { c.UsageLimitDisableAfter = intPtr(2) })
	manager := newCodexPolicyTestManager(t, &RoundRobinSelector{}, cfg, model, &Auth{ID: authID, Provider: "codex"})

	manager.MarkResult(context.Background(), codexUsageLimitResult(authID, model))
	// A repeat inside the active cooldown window is not counted.
	manager.MarkResult(context.Background(), codexUsageLimitResult(authID, model))
	if codexAuthDisabled(manager, authID) {
		t.Fatal("auth disabled while still inside the first cooldown window")
	}
	expireCodexCooldown(manager, authID)
	manager.MarkResult(context.Background(), codexUsageLimitResult(authID, model))
	if !codexAuthDisabled(manager, authID) {
		t.Fatal("auth not disabled after reaching the usage-limit threshold")
	}
}

func TestCodexUsageLimitCounterResetsOnSuccess(t *testing.T) {
	model := "codex-usage-limit-reset"
	authID := "usage-limit-reset-auth"
	cfg := codexFailurePolicyConfig(func(c *internalconfig.CodexConfig) { c.UsageLimitDisableAfter = intPtr(2) })
	manager := newCodexPolicyTestManager(t, &RoundRobinSelector{}, cfg, model, &Auth{ID: authID, Provider: "codex"})

	manager.MarkResult(context.Background(), codexUsageLimitResult(authID, model))
	expireCodexCooldown(manager, authID)
	manager.MarkResult(context.Background(), Result{AuthID: authID, Provider: "codex", Model: model, Success: true})
	manager.MarkResult(context.Background(), codexUsageLimitResult(authID, model))
	if codexAuthDisabled(manager, authID) {
		t.Fatal("auth disabled although a success reset the consecutive counter")
	}
}

func TestCodexAuthFailureDisable(t *testing.T) {
	model := "codex-auth-failure-disable"
	authID := "auth-failure-auth"
	cfg := codexFailurePolicyConfig(func(c *internalconfig.CodexConfig) {
		c.AutoDisableAuthFailures = boolPtr(true)
		c.AuthFailureDisableAfter = intPtr(2)
	})
	manager := newCodexPolicyTestManager(t, &RoundRobinSelector{}, cfg, model, &Auth{ID: authID, Provider: "codex"})
	failure := Result{AuthID: authID, Provider: "codex", Model: model, Error: &Error{HTTPStatus: http.StatusUnauthorized, Message: "invalid or expired token"}}

	manager.MarkResult(context.Background(), failure)
	if codexAuthDisabled(manager, authID) {
		t.Fatal("auth disabled before reaching the auth-failure threshold")
	}
	manager.MarkResult(context.Background(), failure)
	if !codexAuthDisabled(manager, authID) {
		t.Fatal("auth not disabled after reaching the auth-failure threshold")
	}
}

func TestCodexFailurePolicyDefaultsOff(t *testing.T) {
	model := "codex-failure-policy-off"
	authID := "failure-policy-off-auth"
	manager := newCodexPolicyTestManager(t, &RoundRobinSelector{}, &internalconfig.Config{}, model, &Auth{ID: authID, Provider: "codex"})
	unauthorized := Result{AuthID: authID, Provider: "codex", Model: model, Error: &Error{HTTPStatus: http.StatusUnauthorized, Message: "invalid or expired token"}}
	for i := 0; i < 5; i++ {
		manager.MarkResult(context.Background(), codexUsageLimitResult(authID, model))
		expireCodexCooldown(manager, authID)
		manager.MarkResult(context.Background(), unauthorized)
	}
	if codexAuthDisabled(manager, authID) {
		t.Fatal("auth disabled although the failure policy defaults to off")
	}
}

func TestCodexUsageLimitCooldownFallback(t *testing.T) {
	model := "codex-usage-limit-fallback"
	authID := "usage-limit-fallback-auth"
	cfg := codexFailurePolicyConfig(func(c *internalconfig.CodexConfig) { c.UsageLimitCooldownFallbackHours = intPtr(3) })
	manager := newCodexPolicyTestManager(t, &RoundRobinSelector{}, cfg, model, &Auth{ID: authID, Provider: "codex"})

	before := time.Now()
	manager.MarkResult(context.Background(), codexUsageLimitResult(authID, model))
	auth, _ := manager.GetByID(authID)
	state := auth.ModelStates[model]
	if state == nil {
		t.Fatalf("model state missing for %q", model)
	}
	if want := before.Add(3 * time.Hour); state.NextRetryAfter.Before(want) {
		t.Fatalf("NextRetryAfter = %v, want at least %v", state.NextRetryAfter, want)
	}

	// An explicit upstream reset time still wins over the fallback.
	retryAfter := 10 * time.Minute
	result := Result{AuthID: authID, Provider: "codex", Model: model, Error: codexUsageLimitResult(authID, model).Error, RetryAfter: &retryAfter}
	if got := manager.applyCodexUsageLimitFallback(result); got.RetryAfter == nil || *got.RetryAfter != retryAfter {
		t.Fatalf("RetryAfter = %v, want upstream %v", got.RetryAfter, retryAfter)
	}
}
