package auth

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func quotaIdentityAuth(id, email string) *Auth {
	return &Auth{
		ID:       id,
		Provider: "claude",
		Status:   StatusActive,
		Metadata: map[string]any{"email": email, "account_uuid": "uuid-" + email},
	}
}

func TestUpdateClearsQuotaWhenCredentialChangesAccount(t *testing.T) {
	ctx := context.Background()
	manager := NewManager(nil, &RoundRobinSelector{}, nil)
	if _, err := manager.Register(WithSkipPersist(ctx), quotaIdentityAuth("auth-a", "one@example.com")); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	current, _ := manager.GetByID("auth-a")
	current.Quota.Signals = map[string]string{"Anthropic-Ratelimit-Unified-7d-Utilization": "0.9"}
	current.Quota.ObservedAt = time.Now()
	current.ModelStates = map[string]*ModelState{"claude-sonnet": {
		Status: StatusActive,
		Quota:  QuotaState{ObservedAt: time.Now(), Signals: map[string]string{"Anthropic-Ratelimit-Unified-7d-Utilization": "0.9"}},
	}}
	if _, err := manager.Update(WithSkipPersist(ctx), current); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	// Same account, rotated token: the observation stays.
	same, _ := manager.GetByID("auth-a")
	same.Metadata["access_token"] = "rotated"
	if _, err := manager.Update(WithSkipPersist(ctx), same); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if got, _ := manager.GetByID("auth-a"); len(got.Quota.Signals) == 0 || len(got.ModelStates["claude-sonnet"].Quota.Signals) == 0 {
		t.Fatal("quota observation dropped for the same account")
	}

	// The file now holds a different account: the observation is cleared.
	other := quotaIdentityAuth("auth-a", "two@example.com")
	if _, err := manager.Update(WithSkipPersist(ctx), other); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	got, _ := manager.GetByID("auth-a")
	if len(got.Quota.Signals) != 0 || !got.Quota.ObservedAt.IsZero() {
		t.Fatalf("quota after account change = %+v, want cleared", got.Quota)
	}
	if state := got.ModelStates["claude-sonnet"]; state != nil && len(state.Quota.Signals) != 0 {
		t.Fatalf("model quota after account change = %+v, want cleared", state.Quota)
	}
}

func TestMarkResultIgnoresQuotaFromPreviousAccount(t *testing.T) {
	ctx := context.Background()
	manager := NewManager(nil, &RoundRobinSelector{}, nil)
	if _, err := manager.Register(WithSkipPersist(ctx), quotaIdentityAuth("auth-a", "one@example.com")); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	original, _ := manager.GetByID("auth-a")
	requestCtx := withQuotaObservationIdentity(ctx, original)
	if _, err := manager.Update(WithSkipPersist(ctx), quotaIdentityAuth("auth-a", "two@example.com")); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	current, _ := manager.GetByID("auth-a")
	headers := http.Header{"Anthropic-Ratelimit-Unified-7d-Utilization": {"0.99"}}
	observeQuotaHeaders(requestCtx, current, nil, "claude", headers, time.Now())
	if len(current.Quota.Signals) != 0 {
		t.Fatalf("in-flight response for the old account was recorded: %v", current.Quota.Signals)
	}
	observeQuotaHeaders(withQuotaObservationIdentity(ctx, current), current, nil, "claude", headers, time.Now())
	if len(current.Quota.Signals) == 0 {
		t.Fatal("response for the current account was not recorded")
	}
}

func TestRestoreRoutingStateSkipsSnapshotForAnotherAccount(t *testing.T) {
	ctx := context.Background()
	manager := NewManager(nil, &RoundRobinSelector{}, nil)
	if _, err := manager.Register(WithSkipPersist(ctx), quotaIdentityAuth("auth-a", "two@example.com")); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	snapshotAuth := quotaIdentityAuth("auth-a", "one@example.com")
	state := RoutingState{Version: routingStateVersion, Quota: map[string]QuotaSnapshot{
		"auth-a": {
			ObservedAt: time.Now(),
			Signals:    map[string]string{"Anthropic-Ratelimit-Unified-7d-Utilization": "0.9"},
			Account:    quotaAccountIdentity(snapshotAuth),
		},
	}}
	if quotas, _ := manager.RestoreRoutingState(state, time.Now()); quotas != 0 {
		t.Fatalf("restored %d snapshots for a different account, want 0", quotas)
	}
	state.Quota["auth-a"] = QuotaSnapshot{
		ObservedAt: time.Now(),
		Signals:    map[string]string{"Anthropic-Ratelimit-Unified-7d-Utilization": "0.9"},
		Account:    quotaAccountIdentity(quotaIdentityAuth("auth-a", "two@example.com")),
	}
	if quotas, _ := manager.RestoreRoutingState(state, time.Now()); quotas != 1 {
		t.Fatalf("restored %d snapshots for the same account, want 1", quotas)
	}
}
