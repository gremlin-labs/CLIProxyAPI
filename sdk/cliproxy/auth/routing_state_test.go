package auth

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSessionCacheExportRestoreRoundTrip(t *testing.T) {
	now := time.Now()
	source := NewSessionCache(time.Hour)
	defer source.Stop()
	source.SetAliases("auth-a", "claude::s1::m", "claude::parent::m")
	source.Set("claude::s2::m", "auth-b")
	source.Set("claude::gone::m", "auth-removed")

	pins := source.Export(now)
	if len(pins) != 3 {
		t.Fatalf("exported %d pins, want 3", len(pins))
	}

	target := NewSessionCache(time.Hour)
	defer target.Stop()
	target.Set("claude::s2::m", "auth-live") // live binding must win
	restored := target.Restore(pins, now, func(id string) bool { return id != "auth-removed" })
	if restored != 1 {
		t.Fatalf("restored %d pins, want 1 (s1 group only)", restored)
	}
	for _, key := range []string{"claude::s1::m", "claude::parent::m"} {
		if got, _ := target.Get(key); got != "auth-a" {
			t.Fatalf("%s = %q, want auth-a with aliases preserved", key, got)
		}
	}
	if got, _ := target.Get("claude::s2::m"); got != "auth-live" {
		t.Fatalf("live binding overwritten: got %q", got)
	}
	if _, ok := target.Get("claude::gone::m"); ok {
		t.Fatal("binding to a removed credential was restored")
	}
}

func TestSessionCacheRestoreSkipsExpiredAndClampsTTL(t *testing.T) {
	now := time.Now()
	cache := NewSessionCache(10 * time.Minute)
	defer cache.Stop()
	restored := cache.Restore([]SessionPin{
		{Aliases: []string{"expired"}, AuthID: "a", ExpiresAt: now.Add(-time.Second)},
		{Aliases: []string{"long"}, AuthID: "a", ExpiresAt: now.Add(48 * time.Hour)},
	}, now, nil)
	if restored != 1 {
		t.Fatalf("restored %d, want 1", restored)
	}
	pins := cache.Export(now)
	if len(pins) != 1 || pins[0].ExpiresAt.After(now.Add(10*time.Minute)) {
		t.Fatalf("restored pin expiry = %v, want clamped to the cache TTL", pins)
	}
}

func TestManagerRoutingStateRoundTrip(t *testing.T) {
	ctx := context.Background()
	affinity := NewSessionAffinitySelector(&RoundRobinSelector{})
	defer affinity.Stop()
	manager := NewManager(nil, affinity, nil)
	observed := time.Now().Add(-time.Minute).Truncate(time.Second)
	for _, id := range []string{"auth-a", "auth-b"} {
		if _, err := manager.Register(WithSkipPersist(ctx), &Auth{ID: id, Provider: "claude", Status: StatusActive}); err != nil {
			t.Fatalf("Register(%s) error = %v", id, err)
		}
	}
	current, _ := manager.GetByID("auth-a")
	current.Quota.Signals = map[string]string{"Anthropic-Ratelimit-Unified-7d-Utilization": "0.4"}
	current.Quota.ObservedAt = observed
	if _, err := manager.Update(WithSkipPersist(ctx), current); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	affinity.cache.Set("claude::thread::m", "auth-b")

	path := filepath.Join(t.TempDir(), "routing-state.cpa")
	if err := SaveRoutingStateFile(path, manager.ExportRoutingState(time.Now())); err != nil {
		t.Fatalf("SaveRoutingStateFile() error = %v", err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("state file mode = %v, %v; want 0600", info, err)
	}

	// A fresh process: same credentials, no quota data, no bindings.
	freshAffinity := NewSessionAffinitySelector(&RoundRobinSelector{})
	defer freshAffinity.Stop()
	fresh := NewManager(nil, freshAffinity, nil)
	for _, id := range []string{"auth-a", "auth-b"} {
		if _, err := fresh.Register(WithSkipPersist(ctx), &Auth{ID: id, Provider: "claude", Status: StatusActive}); err != nil {
			t.Fatalf("Register(%s) error = %v", id, err)
		}
	}
	state, err := LoadRoutingStateFile(path)
	if err != nil {
		t.Fatalf("LoadRoutingStateFile() error = %v", err)
	}
	quotas, pins := fresh.RestoreRoutingState(state, time.Now())
	if quotas != 1 || pins != 1 {
		t.Fatalf("restored quotas=%d pins=%d, want 1 and 1", quotas, pins)
	}
	restoredAuth, _ := fresh.GetByID("auth-a")
	if restoredAuth.Quota.Signals["Anthropic-Ratelimit-Unified-7d-Utilization"] != "0.4" || !restoredAuth.Quota.ObservedAt.Equal(observed) {
		t.Fatalf("restored quota = %+v", restoredAuth.Quota)
	}
	if got, _ := freshAffinity.cache.Get("claude::thread::m"); got != "auth-b" {
		t.Fatalf("restored binding = %q, want auth-b", got)
	}

	// An older snapshot never replaces a newer live observation.
	newer := time.Now()
	live, _ := fresh.GetByID("auth-a")
	live.Quota.Signals = map[string]string{"Anthropic-Ratelimit-Unified-7d-Utilization": "0.9"}
	live.Quota.ObservedAt = newer
	if _, err := fresh.Update(WithSkipPersist(ctx), live); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if quotas, _ := fresh.RestoreRoutingState(state, time.Now()); quotas != 0 {
		t.Fatalf("stale snapshot overwrote %d newer observations", quotas)
	}
}

func TestLoadRoutingStateFileMissingIsEmpty(t *testing.T) {
	state, err := LoadRoutingStateFile(filepath.Join(t.TempDir(), "absent.cpa"))
	if err != nil || state.Version != 0 || len(state.Pins) != 0 {
		t.Fatalf("LoadRoutingStateFile(missing) = %+v, %v; want empty state", state, err)
	}
}

func TestSetSelectorCarriesSessionBindings(t *testing.T) {
	old := NewSessionAffinitySelector(&RoundRobinSelector{})
	manager := NewManager(nil, old, nil)
	old.cache.Set("claude::thread::m", "auth-a")
	replacement := NewSessionAffinitySelector(&ResetAwareSelector{})
	defer replacement.Stop()
	manager.SetSelector(replacement)
	if got, _ := replacement.cache.Get("claude::thread::m"); got != "auth-a" {
		t.Fatalf("binding after selector swap = %q, want auth-a", got)
	}
}
