package auth

import (
	"strconv"
	"strings"
	"time"
)

const (
	weeklyQuotaWindow = 7 * 24 * time.Hour
	shortQuotaWindow  = 5 * time.Hour
	// Codex windows at least this long are treated as the weekly window; shorter
	// ones as the rolling short window. Codex's "primary" is not always 5 hours.
	weeklyWindowThreshold = 2 * 24 * time.Hour
)

// quotaWindows is a normalized view of a credential's last observed quota
// snapshot (Claude unified rate-limit headers or Codex usage headers).
// Utilizations are fractions in [0, 1+]; -1 means unknown.
type quotaWindows struct {
	observed      bool
	weeklyResetAt time.Time
	weeklyUtil    float64
	shortResetAt  time.Time
	shortUtil     float64
	// exhausted reports an included window at or over its limit, where further
	// requests are rejected or billed as overage / credits.
	exhausted bool
}

// readQuotaWindows interprets auth.Quota.Signals for the routing layer. Windows
// whose reset time has already passed are rolled forward and treated as empty,
// since the snapshot predates the reset.
func readQuotaWindows(auth *Auth, now time.Time) quotaWindows {
	windows := quotaWindows{weeklyUtil: -1, shortUtil: -1}
	if auth == nil || len(auth.Quota.Signals) == 0 {
		return windows
	}
	signals := make(map[string]string, len(auth.Quota.Signals))
	for key, value := range auth.Quota.Signals {
		signals[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
	}
	switch strings.ToLower(strings.TrimSpace(auth.Provider)) {
	case "claude":
		windows.observed = readClaudeQuotaWindows(&windows, signals)
	case "codex":
		windows.observed = readCodexQuotaWindows(&windows, signals, auth.Quota.ObservedAt)
	default:
		return windows
	}
	if !windows.observed {
		return windows
	}

	windows.weeklyResetAt, windows.weeklyUtil = rollQuotaWindow(windows.weeklyResetAt, windows.weeklyUtil, weeklyQuotaWindow, now)
	windows.shortResetAt, windows.shortUtil = rollQuotaWindow(windows.shortResetAt, windows.shortUtil, shortQuotaWindow, now)
	if windows.weeklyUtil >= 1 || windows.shortUtil >= 1 {
		windows.exhausted = true
	}
	return windows
}

func readClaudeQuotaWindows(windows *quotaWindows, signals map[string]string) bool {
	const prefix = "anthropic-ratelimit-unified-"
	found := false
	if util, ok := parseQuotaFloat(signals[prefix+"7d-utilization"]); ok {
		windows.weeklyUtil = util
		found = true
	}
	if reset, ok := parseUnixSeconds(signals[prefix+"7d-reset"]); ok {
		windows.weeklyResetAt = reset
		found = true
	}
	if util, ok := parseQuotaFloat(signals[prefix+"5h-utilization"]); ok {
		windows.shortUtil = util
		found = true
	}
	if reset, ok := parseUnixSeconds(signals[prefix+"5h-reset"]); ok {
		windows.shortResetAt = reset
		found = true
	}
	// A rejected window status means the included allowance is used up even when
	// utilization is not reported.
	if strings.EqualFold(signals[prefix+"7d-status"], "rejected") {
		windows.weeklyUtil = maxFloat(windows.weeklyUtil, 1)
		found = true
	}
	if strings.EqualFold(signals[prefix+"5h-status"], "rejected") {
		windows.shortUtil = maxFloat(windows.shortUtil, 1)
		found = true
	}
	return found
}

func readCodexQuotaWindows(windows *quotaWindows, signals map[string]string, observedAt time.Time) bool {
	found := false
	for _, name := range []string{"primary", "secondary"} {
		prefix := "x-codex-" + name + "-"
		percent, hasPercent := parseQuotaFloat(signals[prefix+"used-percent"])
		reset, hasReset := parseUnixSeconds(signals[prefix+"reset-at"])
		if !hasReset && !observedAt.IsZero() {
			if seconds, ok := parseQuotaFloat(signals[prefix+"reset-after-seconds"]); ok && seconds >= 0 {
				reset, hasReset = observedAt.Add(time.Duration(seconds*float64(time.Second))), true
			}
		}
		if !hasPercent && !hasReset {
			continue
		}
		found = true
		util := -1.0
		if hasPercent {
			util = percent / 100
		}
		windowMinutes, _ := parseQuotaFloat(signals[prefix+"window-minutes"])
		weekly := time.Duration(windowMinutes*float64(time.Minute)) >= weeklyWindowThreshold
		if windowMinutes <= 0 {
			// Without a window length, Codex's documented layout is primary=short, secondary=weekly.
			weekly = name == "secondary"
		}
		if weekly {
			windows.weeklyUtil, windows.weeklyResetAt = util, reset
		} else {
			windows.shortUtil, windows.shortResetAt = util, reset
		}
	}
	// Codex reports a hit limit outright. Remaining credits do not count: the user
	// avoids paid overage, so a credential past its included allowance stays
	// exhausted whatever its credit balance.
	if strings.EqualFold(signals["x-codex-limit-reached"], "true") || strings.EqualFold(signals["x-codex-allowed"], "false") {
		markCodexLimitReached(windows)
		found = true
	}
	return found
}

// markCodexLimitReached attributes a reported hit limit to the fuller window, so
// the flag clears when that window resets rather than sticking to the snapshot.
func markCodexLimitReached(windows *quotaWindows) {
	switch {
	case windows.weeklyUtil < 0 && windows.shortUtil < 0:
		if !windows.weeklyResetAt.IsZero() {
			windows.weeklyUtil = 1
		}
		if !windows.shortResetAt.IsZero() {
			windows.shortUtil = 1
		}
		if windows.weeklyUtil < 0 && windows.shortUtil < 0 {
			// No window is known at all; keep the credential exhausted until the
			// next observation replaces this one.
			windows.weeklyUtil = 1
		}
	case windows.weeklyUtil >= windows.shortUtil:
		windows.weeklyUtil = maxFloat(windows.weeklyUtil, 1)
	default:
		windows.shortUtil = maxFloat(windows.shortUtil, 1)
	}
}

// rollQuotaWindow advances a reset time that has already passed and treats the
// window as empty, because the observed utilization belongs to an earlier window.
func rollQuotaWindow(resetAt time.Time, util float64, period time.Duration, now time.Time) (time.Time, float64) {
	if resetAt.IsZero() || resetAt.After(now) {
		return resetAt, util
	}
	periods := now.Sub(resetAt)/period + 1
	return resetAt.Add(periods * period), 0
}

func parseQuotaFloat(raw string) (float64, bool) {
	if raw == "" {
		return 0, false
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value < 0 {
		return 0, false
	}
	return value, true
}

func parseUnixSeconds(raw string) (time.Time, bool) {
	seconds, ok := parseQuotaFloat(raw)
	if !ok || seconds <= 0 {
		return time.Time{}, false
	}
	return time.Unix(int64(seconds), 0), true
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
