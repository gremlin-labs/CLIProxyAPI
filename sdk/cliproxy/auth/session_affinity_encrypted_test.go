package auth

import (
	"context"
	"testing"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

const encryptedCodexPayload = `{"model":"gpt-5-codex","input":[{"type":"message","role":"user","content":"hi"},{"type":"reasoning","summary":[],"encrypted_content":"gAAAA-opaque"}]}`

func encryptedThreadOpts(session string, payload string) cliproxyexecutor.Options {
	opts := stickyOpts(session)
	opts.OriginalRequest = []byte(payload)
	return opts
}

func encryptedThreadSetup(t *testing.T, now *time.Time, session, payload string) (*SessionAffinitySelector, cliproxyexecutor.Options, []*Auth, *Auth, *Auth) {
	t.Helper()
	selector := stickyTestSelector(now)
	a := &Auth{ID: "a", Provider: "codex", Status: StatusActive}
	b := &Auth{ID: "b", Provider: "codex", Status: StatusActive}
	auths := []*Auth{a, b}
	selector.setAuthLookup(func(id string) (*Auth, bool) {
		for _, auth := range auths {
			if auth.ID == id {
				return auth, true
			}
		}
		return nil, false
	})
	opts := encryptedThreadOpts(session, payload)
	picked, err := selector.Pick(context.Background(), "codex", "gpt-5-codex", opts, auths)
	if err != nil || picked == nil {
		t.Fatalf("initial Pick() = %v, %v", picked, err)
	}
	if picked.ID == "a" {
		return selector, opts, auths, a, b
	}
	return selector, opts, auths, b, a
}

func TestSessionAffinityEncryptedCodexThreadWaitsForBoundCredential(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	selector, opts, auths, pinned, _ := encryptedThreadSetup(t, &now, "thread-enc-1", encryptedCodexPayload)
	defer selector.Stop()

	// A long outage would normally move the pin; an encrypted thread waits instead.
	coolCredential(pinned, now.Add(time.Hour))
	picked, err := selector.Pick(context.Background(), "codex", "gpt-5-codex", opts, auths)
	if picked != nil || err == nil {
		t.Fatalf("Pick() = %v, %v; want a cooldown error", picked, err)
	}
	cooldown, ok := err.(*modelCooldownError)
	if !ok || cooldown.resetIn != time.Hour {
		t.Fatalf("error = %#v, want a model cooldown of 1h", err)
	}

	pinned.Quota = QuotaState{}
	if got, _ := selector.Pick(context.Background(), "codex", "gpt-5-codex", opts, auths); got == nil || got.ID != pinned.ID {
		t.Fatalf("after recovery = %v, want the original pin %s", got, pinned.ID)
	}
}

func TestSessionAffinityEncryptedCodexThreadMovesWhenBoundIsDisabled(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	selector, opts, auths, pinned, other := encryptedThreadSetup(t, &now, "thread-enc-2", encryptedCodexPayload)
	defer selector.Stop()

	pinned.Disabled = true
	pinned.Status = StatusDisabled
	got, err := selector.Pick(context.Background(), "codex", "gpt-5-codex", opts, auths)
	if err != nil || got == nil || got.ID != other.ID {
		t.Fatalf("Pick() = %v, %v; want the thread moved to %s", got, err, other.ID)
	}
}

func TestSessionAffinityPlainCodexThreadStillDetours(t *testing.T) {
	now := time.Unix(1_790_000_000, 0)
	plain := `{"model":"gpt-5-codex","input":[{"type":"message","role":"user","content":"hi"}]}`
	selector, opts, auths, pinned, other := encryptedThreadSetup(t, &now, "thread-enc-3", plain)
	defer selector.Stop()

	coolCredential(pinned, now.Add(time.Hour))
	got, err := selector.Pick(context.Background(), "codex", "gpt-5-codex", opts, auths)
	if err != nil || got == nil || got.ID != other.ID {
		t.Fatalf("Pick() = %v, %v; want a move to %s", got, err, other.ID)
	}
}

func TestCarriesCodexEncryptedState(t *testing.T) {
	for name, tc := range map[string]struct {
		provider string
		payload  string
		want     bool
	}{
		"reasoning item":   {"codex", encryptedCodexPayload, true},
		"compaction item":  {"codex", `{"input":[{"type":"compaction","encrypted_content":"x"}]}`, true},
		"empty ciphertext": {"codex", `{"input":[{"type":"reasoning","encrypted_content":""}]}`, false},
		"not in input":     {"codex", `{"metadata":{"encrypted_content":"x"},"input":[]}`, false},
		"other provider":   {"claude", encryptedCodexPayload, false},
	} {
		if got := carriesCodexEncryptedState(tc.provider, []byte(tc.payload)); got != tc.want {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}
