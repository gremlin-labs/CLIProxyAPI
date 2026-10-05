package auth

import (
	"context"
	"sort"
	"sync"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

const (
	// resetAwareNearLimit is the utilization at which a window is treated as nearly
	// used up; such credentials yield to ones with headroom.
	resetAwareNearLimit = 0.95
	// resetAwareProbeInterval spaces out probes of a credential that has no quota
	// snapshot yet, so a probe that returns no quota headers is not retried on
	// every request.
	resetAwareProbeInterval = 10 * time.Minute
)

// Ranking bands for ResetAwareSelector, best first.
const (
	resetAwareBandProbe = iota
	resetAwareBandHealthy
	resetAwareBandUnobserved
	resetAwareBandNearLimit
	resetAwareBandExhausted
)

// ResetAwareSelector drains the credential whose weekly quota window resets soonest,
// so allowance that would expire unused is spent first.
//
// Within the highest available priority tier, candidates are ranked:
//  1. credentials with no quota snapshot that are due a probe (one request teaches
//     the router their windows);
//  2. credentials with headroom, ordered by weekly reset time (soonest first);
//  3. credentials with no snapshot that were probed recently;
//  4. credentials at or above 95% of their 5-hour or weekly window;
//  5. credentials whose included allowance is exhausted, where requests would be
//     rejected or billed as overage / credits.
//
// Ties fall back to credential ID so selection is deterministic.
type ResetAwareSelector struct {
	mu     sync.Mutex
	probes map[string]time.Time
	// now is overridable in tests.
	now func() time.Time
}

// Pick selects the best-ranked available credential.
func (s *ResetAwareSelector) Pick(ctx context.Context, provider, model string, opts cliproxyexecutor.Options, auths []*Auth) (*Auth, error) {
	_ = opts
	now := s.clock()
	available, err := getSelectorAvailableAuths(ctx, auths, provider, model, now)
	if err != nil {
		return nil, err
	}
	available = preferCodexWebsocketAuths(ctx, provider, available)

	s.mu.Lock()
	defer s.mu.Unlock()
	ranked := s.rankLocked(available, now)
	picked := ranked[0]
	if s.bandLocked(picked, readQuotaWindows(picked, now), now) == resetAwareBandProbe {
		if s.probes == nil {
			s.probes = make(map[string]time.Time)
		}
		s.probes[picked.ID] = now
	}
	return picked, nil
}

func (s *ResetAwareSelector) clock() time.Time {
	if s != nil && s.now != nil {
		return s.now()
	}
	return time.Now()
}

type resetAwareCandidate struct {
	auth    *Auth
	band    int
	windows quotaWindows
}

func (s *ResetAwareSelector) rankLocked(auths []*Auth, now time.Time) []*Auth {
	candidates := make([]resetAwareCandidate, 0, len(auths))
	for _, auth := range auths {
		windows := readQuotaWindows(auth, now)
		candidates = append(candidates, resetAwareCandidate{
			auth:    auth,
			band:    s.bandLocked(auth, windows, now),
			windows: windows,
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.band != b.band {
			return a.band < b.band
		}
		if a.band == resetAwareBandExhausted {
			// Prefer whichever exhausted credential gets relief first.
			if ra, rb := earliestRelief(a.windows), earliestRelief(b.windows); !ra.Equal(rb) {
				return resetBefore(ra, rb)
			}
		} else if !a.windows.weeklyResetAt.Equal(b.windows.weeklyResetAt) {
			return resetBefore(a.windows.weeklyResetAt, b.windows.weeklyResetAt)
		}
		return a.auth.ID < b.auth.ID
	})
	ranked := make([]*Auth, len(candidates))
	for i := range candidates {
		ranked[i] = candidates[i].auth
	}
	return ranked
}

func (s *ResetAwareSelector) bandLocked(auth *Auth, windows quotaWindows, now time.Time) int {
	switch {
	case !windows.observed:
		if last, ok := s.probes[auth.ID]; ok && now.Sub(last) < resetAwareProbeInterval {
			return resetAwareBandUnobserved
		}
		return resetAwareBandProbe
	case windows.exhausted:
		return resetAwareBandExhausted
	case windows.weeklyUtil >= resetAwareNearLimit || windows.shortUtil >= resetAwareNearLimit:
		return resetAwareBandNearLimit
	default:
		return resetAwareBandHealthy
	}
}

// earliestRelief is when an exhausted credential regains included allowance: the
// latest reset among its exhausted windows.
func earliestRelief(windows quotaWindows) time.Time {
	var relief time.Time
	if windows.weeklyUtil >= 1 {
		relief = windows.weeklyResetAt
	}
	if windows.shortUtil >= 1 && windows.shortResetAt.After(relief) {
		relief = windows.shortResetAt
	}
	return relief
}

// resetBefore orders known reset times ascending, with unknown (zero) times last.
func resetBefore(a, b time.Time) bool {
	if a.IsZero() != b.IsZero() {
		return !a.IsZero()
	}
	return a.Before(b)
}
