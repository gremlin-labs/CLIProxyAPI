package modelintel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
)

const (
	// EnvAPIKey is the fallback key source when the config key is empty.
	EnvAPIKey = "ARTIFICIAL_ANALYSIS_API_KEY"
	// DefaultCacheTTL applies when cache-ttl is empty or invalid.
	DefaultCacheTTL = 6 * time.Hour

	// KeySourceConfig and KeySourceEnv report where the active key came from.
	KeySourceConfig = "config"
	KeySourceEnv    = "env"
)

// Settings is the effective configuration for one request.
type Settings struct {
	APIKey    string
	KeySource string
	Tier      string
	CacheTTL  time.Duration
}

// Configured reports whether a key is available.
func (s Settings) Configured() bool { return s.APIKey != "" }

// ResolveSettings applies defaults and the environment fallback to cfg.
func ResolveSettings(cfg config.ModelIntelligenceConfig) Settings {
	return resolveSettings(cfg, os.LookupEnv)
}

func resolveSettings(cfg config.ModelIntelligenceConfig, lookupEnv func(string) (string, bool)) Settings {
	s := Settings{Tier: NormalizeTier(cfg.ArtificialAnalysisTier), CacheTTL: DefaultCacheTTL}
	if key := strings.TrimSpace(cfg.ArtificialAnalysisAPIKey); key != "" {
		s.APIKey, s.KeySource = key, KeySourceConfig
	} else if env, ok := lookupEnv(EnvAPIKey); ok && strings.TrimSpace(env) != "" {
		s.APIKey, s.KeySource = strings.TrimSpace(env), KeySourceEnv
	}
	if ttl, err := time.ParseDuration(strings.TrimSpace(cfg.CacheTTL)); err == nil && ttl > 0 {
		s.CacheTTL = ttl
	}
	return s
}

// NormalizeTier maps a configured tier to TierFree or TierPro.
func NormalizeTier(tier string) string {
	if strings.EqualFold(strings.TrimSpace(tier), TierPro) {
		return TierPro
	}
	return TierFree
}

// ValidTier reports whether tier is an accepted configuration value.
func ValidTier(tier string) bool {
	t := strings.ToLower(strings.TrimSpace(tier))
	return t == TierFree || t == TierPro
}

// Result is a served snapshot plus how it was obtained.
type Result struct {
	Snapshot *Snapshot
	// Cached is true when the snapshot did not come from an upstream call made for this request.
	Cached bool
	// Warning is set when stale data is served because upstream refused the refresh.
	Warning string
	// RetryAfter is the upstream Retry-After (seconds) accompanying Warning.
	RetryAfter int
}

type entry struct {
	snap    *Snapshot
	expires time.Time
}

type call struct {
	done chan struct{}
	snap *Snapshot
	err  error
}

// Service caches snapshots per tier and key, and collapses concurrent fetches.
type Service struct {
	baseURL string
	now     func() time.Time

	mu       sync.Mutex
	gen      uint64
	entries  map[string]entry
	inflight map[string]*call
	// joins counts callers that shared an in-flight fetch (observability for tests).
	joins int
}

// NewService returns a Service targeting baseURL (DefaultBaseURL when empty).
// now may be nil to use the wall clock.
func NewService(baseURL string, now func() time.Time) *Service {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if now == nil {
		now = time.Now
	}
	return &Service{baseURL: baseURL, now: now, entries: make(map[string]entry), inflight: make(map[string]*call)}
}

// Clear drops cached data; in-flight fetches finish but are not stored.
func (s *Service) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gen++
	s.entries = make(map[string]entry)
	s.inflight = make(map[string]*call)
}

// cacheKey separates tiers and keys so a key change never serves another key's data.
func cacheKey(st Settings) string {
	sum := sha256.Sum256([]byte(st.APIKey))
	return st.Tier + ":" + hex.EncodeToString(sum[:8])
}

// Get returns a cached snapshot when fresh, otherwise fetches. refresh bypasses
// the fresh-cache check. When upstream rate-limits a refresh and earlier data
// exists, that data is returned with Warning set and a nil error.
func (s *Service) Get(ctx context.Context, client *http.Client, st Settings, refresh bool) (Result, error) {
	key := cacheKey(st)
	s.mu.Lock()
	cached, hasCached := s.entries[key]
	if hasCached && !refresh && s.now().Before(cached.expires) {
		s.mu.Unlock()
		return Result{Snapshot: cached.snap, Cached: true}, nil
	}
	c, joined := s.inflight[key]
	if joined {
		s.joins++
	} else {
		c = &call{done: make(chan struct{})}
		s.inflight[key] = c
		gen := s.gen
		go s.run(ctx, client, st, key, gen, c)
	}
	s.mu.Unlock()

	select {
	case <-c.done:
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
	if c.err != nil {
		if e, ok := IsUpstreamError(c.err); ok && e.RateLimited() && hasCached {
			return Result{
				Snapshot:   cached.snap,
				Cached:     true,
				Warning:    "Artificial Analysis rate limit reached; serving previously fetched data",
				RetryAfter: e.RetryAfter,
			}, nil
		}
		return Result{}, c.err
	}
	return Result{Snapshot: c.snap, Cached: joined}, nil
}

// run performs the fetch bound to the initiating request's context.
func (s *Service) run(ctx context.Context, client *http.Client, st Settings, key string, gen uint64, c *call) {
	snap, err := fetchAll(ctx, client, s.baseURL, st.APIKey, st.Tier, s.now())
	s.mu.Lock()
	if s.inflight[key] == c {
		delete(s.inflight, key)
	}
	if err == nil && s.gen == gen {
		s.entries[key] = entry{snap: snap, expires: s.now().Add(st.CacheTTL)}
	}
	s.mu.Unlock()
	c.snap, c.err = snap, err
	close(c.done)
}
