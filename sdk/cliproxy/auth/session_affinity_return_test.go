package auth

import (
	"context"
	"testing"
	"time"
)

func returnTestSelector(now *time.Time, enabled bool) *SessionAffinitySelector {
	selector := NewSessionAffinitySelectorWithConfig(SessionAffinityConfig{
		Fallback:          &RoundRobinSelector{},
		TTL:               time.Hour,
		ReturnToPreferred: enabled,
	})
	selector.now = func() time.Time { return *now }
	return selector
}

func priorityAuth(id, priority string) *Auth {
	return &Auth{ID: id, Provider: "claude", Status: StatusActive, Attributes: map[string]string{"priority": priority}}
}

// failOverToBackup binds a thread to the preferred credential, then takes it out
// long enough for the binding to move to the backup.
func failOverToBackup(t *testing.T, selector *SessionAffinitySelector, now time.Time, preferred, backup *Auth, session string) {
	t.Helper()
	auths := []*Auth{preferred, backup}
	opts := stickyOpts(session)
	if got := mustPick(t, selector, opts, auths); got != preferred.ID {
		t.Fatalf("cold bind = %s, want the higher-priority %s", got, preferred.ID)
	}
	coolCredential(preferred, now.Add(time.Hour))
	if got := mustPick(t, selector, opts, auths); got != backup.ID {
		t.Fatalf("during outage = %s, want %s", got, backup.ID)
	}
	preferred.Quota = QuotaState{}
}

func TestSessionAffinityReturnsToPreferredCredentialWhenEnabled(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	selector := returnTestSelector(&now, true)
	defer selector.Stop()
	preferred, backup := priorityAuth("preferred", "10"), priorityAuth("backup", "0")
	failOverToBackup(t, selector, now, preferred, backup, "thread-return-1")

	opts := stickyOpts("thread-return-1")
	auths := []*Auth{preferred, backup}
	for i := 0; i < 3; i++ {
		if got := mustPick(t, selector, opts, auths); got != preferred.ID {
			t.Fatalf("pick %d after recovery = %s, want the thread back on %s", i, got, preferred.ID)
		}
	}
}

func TestSessionAffinityStaysOnBackupByDefault(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	selector := returnTestSelector(&now, false)
	defer selector.Stop()
	preferred, backup := priorityAuth("preferred", "10"), priorityAuth("backup", "0")
	failOverToBackup(t, selector, now, preferred, backup, "thread-return-2")

	if got := mustPick(t, selector, stickyOpts("thread-return-2"), []*Auth{preferred, backup}); got != backup.ID {
		t.Fatalf("after recovery = %s, want the binding kept on %s (opt-in only)", got, backup.ID)
	}
}

func TestSessionAffinityReturnNeverMovesBetweenEqualPriorities(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	selector := returnTestSelector(&now, true)
	defer selector.Stop()
	a, b := priorityAuth("a", "5"), priorityAuth("b", "5")
	auths := []*Auth{a, b}
	opts := stickyOpts("thread-return-3")
	bound := mustPick(t, selector, opts, auths)
	for i := 0; i < 6; i++ {
		if got := mustPick(t, selector, opts, auths); got != bound {
			t.Fatalf("pick %d = %s, want the binding kept on %s", i, got, bound)
		}
	}
}

func TestSessionAffinityReturnSkipsEncryptedCodexThreads(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	selector := returnTestSelector(&now, true)
	defer selector.Stop()
	preferred := &Auth{ID: "preferred", Provider: "codex", Status: StatusActive, Attributes: map[string]string{"priority": "10"}}
	backup := &Auth{ID: "backup", Provider: "codex", Status: StatusActive}
	auths := []*Auth{preferred, backup}
	opts := encryptedThreadOpts("thread-return-4", encryptedCodexPayload)

	// The thread starts on the backup while the preferred credential is disabled.
	preferred.Disabled, preferred.Status = true, StatusDisabled
	if got, err := selector.Pick(context.Background(), "codex", "gpt-5-codex", opts, auths); err != nil || got == nil || got.ID != backup.ID {
		t.Fatalf("cold bind = %v, %v; want %s", got, err, backup.ID)
	}
	preferred.Disabled, preferred.Status = false, StatusActive

	got, err := selector.Pick(context.Background(), "codex", "gpt-5-codex", opts, auths)
	if err != nil || got == nil || got.ID != backup.ID {
		t.Fatalf("Pick() = %v, %v; want the encrypted thread kept on %s", got, err, backup.ID)
	}
}
