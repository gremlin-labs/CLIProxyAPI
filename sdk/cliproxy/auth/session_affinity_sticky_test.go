package auth

import (
	"context"
	"net/http"
	"testing"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

func stickyTestSelector(now *time.Time) *SessionAffinitySelector {
	selector := NewSessionAffinitySelectorWithConfig(SessionAffinityConfig{
		Fallback: &RoundRobinSelector{},
		TTL:      time.Hour,
	})
	selector.now = func() time.Time { return *now }
	return selector
}

func stickyOpts(session string) cliproxyexecutor.Options {
	return cliproxyexecutor.Options{Metadata: map[string]any{
		cliproxyexecutor.DerivedSessionIDMetadataKey: session,
	}}
}

func coolCredential(auth *Auth, until time.Time) {
	auth.Quota = QuotaState{Exceeded: true, Reason: "credential_quota", NextRecoverAt: until}
}

func mustPick(t *testing.T, selector *SessionAffinitySelector, opts cliproxyexecutor.Options, auths []*Auth) string {
	t.Helper()
	picked, err := selector.Pick(context.Background(), "claude", "claude-test", opts, auths)
	if err != nil || picked == nil {
		t.Fatalf("Pick() = %v, %v", picked, err)
	}
	return picked.ID
}

func TestSessionAffinityBriefOutageDetoursConsistentlyAndReturns(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	selector := stickyTestSelector(&now)
	defer selector.Stop()
	a := &Auth{ID: "a", Provider: "claude", Status: StatusActive}
	b := &Auth{ID: "b", Provider: "claude", Status: StatusActive}
	c := &Auth{ID: "c", Provider: "claude", Status: StatusActive}
	auths := []*Auth{a, b, c}
	opts := stickyOpts("thread-1")

	bound := mustPick(t, selector, opts, auths)
	var pinned *Auth
	for _, auth := range auths {
		if auth.ID == bound {
			pinned = auth
		}
	}
	coolCredential(pinned, now.Add(2*time.Minute))

	detour := mustPick(t, selector, opts, auths)
	if detour == bound {
		t.Fatalf("detour = %s, want a different credential while %s cools", detour, bound)
	}
	for i := 0; i < 3; i++ {
		if got := mustPick(t, selector, opts, auths); got != detour {
			t.Fatalf("detour pick %d = %s, want the same detour %s (no scattering)", i, got, detour)
		}
	}
	pinned.Quota = QuotaState{}
	if got := mustPick(t, selector, opts, auths); got != bound {
		t.Fatalf("after recovery = %s, want the original pin %s", got, bound)
	}
}

func TestSessionAffinityLongOutageMovesPin(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	selector := stickyTestSelector(&now)
	defer selector.Stop()
	a := &Auth{ID: "a", Provider: "claude", Status: StatusActive}
	b := &Auth{ID: "b", Provider: "claude", Status: StatusActive}
	auths := []*Auth{a, b}
	opts := stickyOpts("thread-2")

	bound := mustPick(t, selector, opts, auths)
	pinned, other := a, b
	if bound == "b" {
		pinned, other = b, a
	}
	coolCredential(pinned, now.Add(time.Hour))
	if got := mustPick(t, selector, opts, auths); got != other.ID {
		t.Fatalf("pick during long outage = %s, want %s", got, other.ID)
	}
	pinned.Quota = QuotaState{}
	if got := mustPick(t, selector, opts, auths); got != other.ID {
		t.Fatalf("after long outage = %s, want the moved pin %s", got, other.ID)
	}
}

func TestSessionAffinityRepinsAfterGraceAway(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	selector := stickyTestSelector(&now)
	defer selector.Stop()
	a := &Auth{ID: "a", Provider: "claude", Status: StatusActive}
	b := &Auth{ID: "b", Provider: "claude", Status: StatusActive}
	auths := []*Auth{a, b}
	opts := stickyOpts("thread-3")

	bound := mustPick(t, selector, opts, auths)
	pinned, other := a, b
	if bound == "b" {
		pinned, other = b, a
	}
	// Repeatedly short cooldowns that never clear: once away for the grace period, move.
	coolCredential(pinned, now.Add(time.Minute))
	mustPick(t, selector, opts, auths)
	now = now.Add(sessionAffinityRepinGrace + time.Second)
	coolCredential(pinned, now.Add(time.Minute))
	mustPick(t, selector, opts, auths)
	pinned.Quota = QuotaState{}
	if got := mustPick(t, selector, opts, auths); got != other.ID {
		t.Fatalf("after %v away = %s, want the pin moved to %s", sessionAffinityRepinGrace, got, other.ID)
	}
}

func TestSessionAffinityRemovedCredentialRepinsImmediately(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	selector := stickyTestSelector(&now)
	defer selector.Stop()
	a := &Auth{ID: "a", Provider: "claude", Status: StatusActive}
	b := &Auth{ID: "b", Provider: "claude", Status: StatusActive}
	opts := stickyOpts("thread-4")
	bound := mustPick(t, selector, opts, []*Auth{a, b})
	remaining := a
	if bound == "a" {
		remaining = b
	}
	selector.setAuthLookup(func(id string) (*Auth, bool) {
		if id == remaining.ID {
			return remaining, true
		}
		return nil, false
	})
	if got := mustPick(t, selector, opts, []*Auth{remaining}); got != remaining.ID {
		t.Fatalf("pick = %s, want %s", got, remaining.ID)
	}
	// The removed credential reappearing must not pull the thread back.
	if got := mustPick(t, selector, opts, []*Auth{a, b}); got != remaining.ID {
		t.Fatalf("after removal = %s, want the moved pin %s", got, remaining.ID)
	}
}

func TestSessionAffinityTransientFailureKeepsBinding(t *testing.T) {
	short := time.Minute
	long := time.Hour
	cases := []struct {
		name string
		res  Result
		keep bool
	}{
		{"network error", Result{Error: &Error{Message: "connection reset"}}, true},
		{"server error", Result{Error: &Error{HTTPStatus: http.StatusBadGateway}}, true},
		{"short rate limit", Result{Error: &Error{HTTPStatus: http.StatusTooManyRequests}, RetryAfter: &short}, true},
		{"rate limit without hint", Result{Error: &Error{HTTPStatus: http.StatusTooManyRequests}}, true},
		{"long rate limit", Result{Error: &Error{HTTPStatus: http.StatusTooManyRequests}, RetryAfter: &long}, false},
		{"unauthorized", Result{Error: &Error{HTTPStatus: http.StatusUnauthorized}}, false},
		{"forbidden", Result{Error: &Error{HTTPStatus: http.StatusForbidden}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sessionAffinityTransientFailure(tc.res); got != tc.keep {
				t.Fatalf("sessionAffinityTransientFailure = %v, want %v", got, tc.keep)
			}
		})
	}
}
