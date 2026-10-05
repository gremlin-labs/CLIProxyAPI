package auth

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/registry"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

var resetAwareNow = time.Unix(1_790_000_000, 0)

func unix(t time.Time) string { return strconv.FormatInt(t.Unix(), 10) }

func claudeAuth(id string, weeklyUtil, shortUtil float64, weeklyReset time.Duration) *Auth {
	return &Auth{ID: id, Provider: "claude", Status: StatusActive, Quota: QuotaState{
		ObservedAt: resetAwareNow.Add(-time.Minute),
		Signals: map[string]string{
			"Anthropic-Ratelimit-Unified-7d-Utilization": strconv.FormatFloat(weeklyUtil, 'f', -1, 64),
			"Anthropic-Ratelimit-Unified-7d-Reset":       unix(resetAwareNow.Add(weeklyReset)),
			"Anthropic-Ratelimit-Unified-5h-Utilization": strconv.FormatFloat(shortUtil, 'f', -1, 64),
			"Anthropic-Ratelimit-Unified-5h-Reset":       unix(resetAwareNow.Add(2 * time.Hour)),
		},
	}}
}

func newTestResetAwareSelector() *ResetAwareSelector {
	return &ResetAwareSelector{now: func() time.Time { return resetAwareNow }}
}

func pickID(t *testing.T, s *ResetAwareSelector, provider string, auths ...*Auth) string {
	t.Helper()
	picked, err := s.Pick(context.Background(), provider, "", cliproxyexecutor.Options{}, auths)
	if err != nil {
		t.Fatalf("Pick() error = %v", err)
	}
	return picked.ID
}

func TestResetAwarePrefersSoonestWeeklyReset(t *testing.T) {
	s := newTestResetAwareSelector()
	in72h := claudeAuth("a-72h", 0.10, 0.10, 72*time.Hour)
	in24h := claudeAuth("b-24h", 0.40, 0.10, 24*time.Hour)
	for i := 0; i < 3; i++ {
		if got := pickID(t, s, "claude", in72h, in24h); got != "b-24h" {
			t.Fatalf("pick %d = %s, want the account resetting in 24h", i, got)
		}
	}
}

func TestResetAwareYieldsNearLimitAndExhaustedAccounts(t *testing.T) {
	s := newTestResetAwareSelector()
	soonButShortFull := claudeAuth("a-soon-5h-full", 0.20, 0.97, 12*time.Hour)
	soonButWeeklyFull := claudeAuth("b-soon-weekly-full", 0.96, 0.10, 6*time.Hour)
	later := claudeAuth("c-later", 0.20, 0.10, 100*time.Hour)
	if got := pickID(t, s, "claude", soonButShortFull, soonButWeeklyFull, later); got != "c-later" {
		t.Fatalf("picked %s, want the account with headroom despite its later reset", got)
	}

	exhausted := claudeAuth("a-exhausted", 1.0, 0.30, 1*time.Hour)
	near := claudeAuth("b-near", 0.97, 0.30, 50*time.Hour)
	if got := pickID(t, s, "claude", exhausted, near); got != "b-near" {
		t.Fatalf("picked %s, want near-limit over exhausted (which would spend overage)", got)
	}
}

func TestResetAwareTreatsRejectedStatusAsExhausted(t *testing.T) {
	s := newTestResetAwareSelector()
	rejected := claudeAuth("a-rejected", 0.50, 0.50, 1*time.Hour)
	rejected.Quota.Signals["Anthropic-Ratelimit-Unified-5h-Status"] = "rejected"
	healthy := claudeAuth("b-healthy", 0.50, 0.50, 90*time.Hour)
	if got := pickID(t, s, "claude", rejected, healthy); got != "b-healthy" {
		t.Fatalf("picked %s, want the account whose 5h window is not rejected", got)
	}
}

func TestResetAwareProbesUnobservedAccountsOnce(t *testing.T) {
	s := newTestResetAwareSelector()
	known := claudeAuth("a-known", 0.10, 0.10, 24*time.Hour)
	fresh := &Auth{ID: "z-fresh", Provider: "claude", Status: StatusActive}
	if got := pickID(t, s, "claude", known, fresh); got != "z-fresh" {
		t.Fatalf("first pick = %s, want the unobserved account probed", got)
	}
	if got := pickID(t, s, "claude", known, fresh); got != "a-known" {
		t.Fatalf("second pick = %s, want ranked accounts while the probe is recent", got)
	}
	s.now = func() time.Time { return resetAwareNow.Add(resetAwareProbeInterval + time.Second) }
	if got := pickID(t, s, "claude", known, fresh); got != "z-fresh" {
		t.Fatalf("pick after the probe interval = %s, want the still-unobserved account probed again", got)
	}
}

func TestResetAwareRollsOverPassedResets(t *testing.T) {
	s := newTestResetAwareSelector()
	// Snapshot from an earlier week: 99% used, but that window reset an hour ago,
	// so the account now has a full week ahead of it (resets in ~167h).
	stale := claudeAuth("a-stale", 0.99, 0.10, -1*time.Hour)
	other := claudeAuth("b-other", 0.30, 0.10, 170*time.Hour)
	if got := pickID(t, s, "claude", stale, other); got != "a-stale" {
		t.Fatalf("picked %s, want the rolled-over account (healthy, resets at +167h)", got)
	}
	windows := readQuotaWindows(stale, resetAwareNow)
	if windows.weeklyUtil != 0 || !windows.weeklyResetAt.After(resetAwareNow) {
		t.Fatalf("rolled window = util %v reset %v, want util 0 and a future reset", windows.weeklyUtil, windows.weeklyResetAt)
	}
}

func TestReadQuotaWindowsCodexClassifiesByWindowLength(t *testing.T) {
	auth := &Auth{ID: "codex", Provider: "codex", Quota: QuotaState{
		ObservedAt: resetAwareNow,
		Signals: map[string]string{
			// Primary here is the weekly window (10080 minutes), as in real Codex responses.
			"X-Codex-Primary-Used-Percent":           "51",
			"X-Codex-Primary-Window-Minutes":         "10080",
			"X-Codex-Primary-Reset-After-Seconds":    "86400",
			"X-Codex-Secondary-Used-Percent":         "20",
			"X-Codex-Secondary-Window-Minutes":       "300",
			"X-Codex-Secondary-Reset-At":             unix(resetAwareNow.Add(3 * time.Hour)),
			"X-Codex-Bengalfox-Primary-Used-Percent": "99",
		},
	}}
	windows := readQuotaWindows(auth, resetAwareNow)
	if !windows.observed || windows.weeklyUtil != 0.51 || windows.shortUtil != 0.20 {
		t.Fatalf("windows = %+v, want weekly 0.51 and short 0.20", windows)
	}
	if !windows.weeklyResetAt.Equal(resetAwareNow.Add(24 * time.Hour)) {
		t.Fatalf("weekly reset = %v, want observedAt + reset-after-seconds", windows.weeklyResetAt)
	}
	if windows.exhausted {
		t.Fatal("a per-model additional limit must not mark the whole credential exhausted")
	}

	auth.Quota.Signals["X-Codex-Secondary-Used-Percent"] = "100"
	if !readQuotaWindows(auth, resetAwareNow).exhausted {
		t.Fatal("100% of an included Codex window should count as exhausted (credits territory)")
	}
}

func TestResetAwareKeepsHighestPriorityTier(t *testing.T) {
	s := newTestResetAwareSelector()
	low := claudeAuth("a-low-priority", 0.10, 0.10, 1*time.Hour)
	low.Attributes = map[string]string{"priority": "0"}
	high := claudeAuth("b-high-priority", 0.10, 0.10, 150*time.Hour)
	high.Attributes = map[string]string{"priority": "10"}
	if got := pickID(t, s, "claude", low, high); got != "b-high-priority" {
		t.Fatalf("picked %s, want priority to outrank reset time", got)
	}
}

func TestManagerRoutesWithResetAwareSelector(t *testing.T) {
	ctx := context.Background()
	manager := NewManager(nil, &ResetAwareSelector{}, nil)
	manager.RegisterExecutor(&refreshMockExecutor{id: "claude"})
	// Real-clock windows: a-72h resets in 72h, b-24h in 24h.
	now := time.Now()
	for _, spec := range []struct {
		id    string
		reset time.Duration
	}{{"a-72h", 72 * time.Hour}, {"b-24h", 24 * time.Hour}} {
		auth := &Auth{ID: spec.id, Provider: "claude", Status: StatusActive}
		registry.GetGlobalRegistry().RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: "claude-test"}})
		id := auth.ID
		t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(id) })
		if _, err := manager.Register(ctx, auth); err != nil {
			t.Fatalf("Register(%s) error = %v", spec.id, err)
		}
		manager.MarkResult(ctx, Result{AuthID: spec.id, Provider: "claude", Model: "claude-test", Success: true})
		current, _ := manager.GetByID(spec.id)
		current.Quota.Signals = map[string]string{
			"Anthropic-Ratelimit-Unified-7d-Utilization": "0.2",
			"Anthropic-Ratelimit-Unified-7d-Reset":       strconv.FormatInt(now.Add(spec.reset).Unix(), 10),
			"Anthropic-Ratelimit-Unified-5h-Utilization": "0.1",
		}
		current.Quota.ObservedAt = now
		if _, err := manager.Update(ctx, current); err != nil {
			t.Fatalf("Update(%s) error = %v", spec.id, err)
		}
	}
	for i := 0; i < 3; i++ {
		picked, _, err := manager.pickNext(ctx, "claude", "claude-test", cliproxyexecutor.Options{}, nil)
		if err != nil || picked == nil {
			t.Fatalf("pickNext() = %v, %v", picked, err)
		}
		if picked.ID != "b-24h" {
			t.Fatalf("pick %d = %s, want b-24h (soonest weekly reset)", i, picked.ID)
		}
	}
}

func TestReadQuotaWindowsCodexLimitReached(t *testing.T) {
	codex := func(signals map[string]string) *Auth {
		return &Auth{ID: "codex", Provider: "codex", Quota: QuotaState{ObservedAt: resetAwareNow, Signals: signals}}
	}
	base := func() map[string]string {
		return map[string]string{
			"X-Codex-Primary-Used-Percent":     "40",
			"X-Codex-Primary-Window-Minutes":   "10080",
			"X-Codex-Primary-Reset-At":         unix(resetAwareNow.Add(48 * time.Hour)),
			"X-Codex-Secondary-Used-Percent":   "97",
			"X-Codex-Secondary-Window-Minutes": "300",
			"X-Codex-Secondary-Reset-At":       unix(resetAwareNow.Add(2 * time.Hour)),
		}
	}

	signals := base()
	signals["X-Codex-Limit-Reached"] = "true"
	// Credits remaining must not make the credential eligible again.
	signals["X-Codex-Credits-Has-Credits"] = "true"
	signals["X-Codex-Credits-Balance"] = "500"
	windows := readQuotaWindows(codex(signals), resetAwareNow)
	if !windows.exhausted || windows.shortUtil < 1 || windows.weeklyUtil != 0.4 {
		t.Fatalf("windows = %+v, want the fuller (short) window marked exhausted", windows)
	}
	if relief := earliestRelief(windows); !relief.Equal(resetAwareNow.Add(2 * time.Hour)) {
		t.Fatalf("relief = %v, want the short window reset", relief)
	}
	// Once that window has reset, the stale flag no longer applies.
	if later := readQuotaWindows(codex(signals), resetAwareNow.Add(3*time.Hour)); later.exhausted {
		t.Fatalf("windows after reset = %+v, want not exhausted", later)
	}

	signals = base()
	signals["X-Codex-Allowed"] = "false"
	if !readQuotaWindows(codex(signals), resetAwareNow).exhausted {
		t.Fatal("x-codex-allowed=false should count as exhausted")
	}

	signals = map[string]string{"X-Codex-Limit-Reached": "true"}
	windows = readQuotaWindows(codex(signals), resetAwareNow)
	if !windows.observed || !windows.exhausted {
		t.Fatalf("windows = %+v, want a bare limit-reached flag to mark the credential exhausted", windows)
	}

	signals = base()
	signals["X-Codex-Limit-Reached"] = "false"
	if readQuotaWindows(codex(signals), resetAwareNow).exhausted {
		t.Fatal("limit-reached=false must not mark the credential exhausted")
	}
}
