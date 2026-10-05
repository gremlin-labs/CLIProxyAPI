package auth

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func usageLimitState(retry, updated time.Time) *ModelState {
	return &ModelState{
		Status:         StatusError,
		StatusMessage:  "usage_limit_reached",
		Unavailable:    true,
		NextRetryAfter: retry,
		LastError:      &Error{HTTPStatus: http.StatusTooManyRequests, Message: "usage_limit_reached"},
		Quota:          QuotaState{Exceeded: true, Reason: "quota", NextRecoverAt: retry},
		UpdatedAt:      updated,
	}
}

func TestManagerResetQuotaUsageLimitBeforePreservesNewerFailure(t *testing.T) {
	manager := NewManager(nil, nil, nil)
	observedAt := time.Now()
	auth := &Auth{
		ID:       "recover-codex-guarded",
		Provider: "codex",
		ModelStates: map[string]*ModelState{
			"old-model": usageLimitState(observedAt.Add(time.Hour), observedAt.Add(-time.Minute)),
			"new-model": usageLimitState(observedAt.Add(2*time.Hour), observedAt.Add(time.Minute)),
		},
	}
	if _, err := manager.Register(WithSkipPersist(context.Background()), auth); err != nil {
		t.Fatalf("register auth: %v", err)
	}

	_, models, err := manager.ResetQuotaUsageLimitBefore(context.Background(), auth.ID, observedAt)
	if err != nil {
		t.Fatalf("recover quota: %v", err)
	}
	if len(models) != 1 || models[0] != "old-model" {
		t.Fatalf("cleared models = %v, want [old-model]", models)
	}
	updated, _ := manager.GetByID(auth.ID)
	if state := updated.ModelStates["old-model"]; state.Unavailable || state.Quota.Exceeded {
		t.Fatalf("old state = %+v, want cooldown cleared", state)
	}
	if state := updated.ModelStates["new-model"]; !state.Unavailable || !state.Quota.Exceeded {
		t.Fatalf("newer state was cleared: %+v", state)
	}
}

func TestManagerResetQuotaUsageLimitBeforeKeepsNonQuotaFailures(t *testing.T) {
	manager := NewManager(nil, nil, nil)
	observedAt := time.Now()
	retry := observedAt.Add(30 * time.Minute)
	auth := &Auth{
		ID:       "recover-codex-auth-failure",
		Provider: "codex",
		ModelStates: map[string]*ModelState{
			"quota-model": usageLimitState(observedAt.Add(time.Hour), observedAt.Add(-time.Minute)),
			"forbidden-model": {
				Status:         StatusError,
				StatusMessage:  "forbidden",
				Unavailable:    true,
				NextRetryAfter: retry,
				LastError:      &Error{HTTPStatus: http.StatusForbidden, Message: "forbidden"},
				UpdatedAt:      observedAt.Add(-time.Minute),
			},
		},
	}
	if _, err := manager.Register(WithSkipPersist(context.Background()), auth); err != nil {
		t.Fatalf("register auth: %v", err)
	}

	if _, _, err := manager.ResetQuotaUsageLimitBefore(context.Background(), auth.ID, observedAt); err != nil {
		t.Fatalf("recover quota: %v", err)
	}
	updated, _ := manager.GetByID(auth.ID)
	if state := updated.ModelStates["quota-model"]; state.Unavailable || state.Quota.Exceeded {
		t.Fatalf("quota state = %+v, want cleared", state)
	}
	if state := updated.ModelStates["forbidden-model"]; !state.Unavailable || state.LastError == nil {
		t.Fatalf("non-quota failure was cleared: %+v", state)
	}
}

func TestManagerResetQuotaUsageLimitBeforeClearsCredentialQuota(t *testing.T) {
	manager := NewManager(nil, nil, nil)
	observedAt := time.Now()
	next := observedAt.Add(time.Hour)
	sibling := usageLimitState(next, observedAt.Add(-time.Minute))
	sibling.StatusMessage = ""
	sibling.LastError = nil
	sibling.Quota.Reason = "credential_quota"
	auth := &Auth{
		ID:             "recover-codex-credential",
		Provider:       "codex",
		Unavailable:    true,
		NextRetryAfter: next,
		Quota:          QuotaState{Exceeded: true, Reason: "credential_quota", NextRecoverAt: next},
		ModelStates: map[string]*ModelState{
			"hit-model":     usageLimitState(next, observedAt.Add(-time.Minute)),
			"sibling-model": sibling,
		},
	}
	if _, err := manager.Register(WithSkipPersist(context.Background()), auth); err != nil {
		t.Fatalf("register auth: %v", err)
	}

	if _, _, err := manager.ResetQuotaUsageLimitBefore(context.Background(), auth.ID, observedAt); err != nil {
		t.Fatalf("recover quota: %v", err)
	}
	updated, _ := manager.GetByID(auth.ID)
	if updated.Unavailable || updated.Quota.Exceeded {
		t.Fatalf("auth = unavailable %v quota %+v, want credential quota cleared", updated.Unavailable, updated.Quota)
	}
	for model, state := range updated.ModelStates {
		if state.Unavailable || state.Quota.Exceeded {
			t.Fatalf("%s state = %+v, want cleared", model, state)
		}
	}
}

func TestManagerResetQuotaUsageLimitBeforeRejectsNonCodex(t *testing.T) {
	manager := NewManager(nil, nil, nil)
	auth := &Auth{ID: "recover-claude", Provider: "claude"}
	if _, err := manager.Register(WithSkipPersist(context.Background()), auth); err != nil {
		t.Fatalf("register auth: %v", err)
	}
	if _, _, err := manager.ResetQuotaUsageLimitBefore(context.Background(), auth.ID, time.Now()); err == nil {
		t.Fatal("ResetQuotaUsageLimitBefore() error = nil for a claude auth")
	}
}
