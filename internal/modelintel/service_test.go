package modelintel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/internal/config"
)

const pageOne = `{
  "tier": "free",
  "intelligence_index_version": "4.0",
  "pagination": {"page": 1, "page_size": 2, "total_pages": 2, "has_more": true},
  "data": [
    {
      "id": "m1", "name": "Model One", "slug": "model-one", "release_date": "2026-01-02",
      "model_creator": {"id": "c1", "name": "Acme", "country": "US"},
      "reasoning_model": true,
      "evaluations": {"artificial_analysis_intelligence_index": 61.5, "artificial_analysis_coding_index": 55, "artificial_analysis_agentic_index": null, "other": 3},
      "artificial_analysis_intelligence_index_cost": {"total_cost": 812.4},
      "pricing": {"price_1m_input_tokens": 1, "price_1m_output_tokens": 5},
      "performance": {"median_output_tokens_per_second": 120.5, "median_time_to_first_token_seconds": 0.8, "median_end_to_end_response_time_seconds": 9.1},
      "context_window_tokens": 200000,
      "licensing": {"is_open_weights": false},
      "openrouter_api_id": "acme/model-one"
    },
    {
      "id": "m2", "name": "Model Two", "slug": "model-two", "release_date": null,
      "model_creator": {"id": "c2", "name": "Beta"},
      "reasoning_model": false,
      "evaluations": {"artificial_analysis_intelligence_index": null},
      "artificial_analysis_intelligence_index_cost": null,
      "pricing": {"price_1m_input_tokens": 2, "price_1m_output_tokens": null, "price_1m_blended_3_to_1": 3.5},
      "performance": null,
      "licensing": {"is_open_weights": true}
    }
  ]
}`

const pageTwo = `{
  "tier": "free",
  "pagination": {"page": 2, "page_size": 2, "total_pages": 2, "has_more": false},
  "data": [{"id": "m3", "name": "Model Three", "slug": "model-three"}]
}`

type fakeAA struct {
	t        *testing.T
	calls    atomic.Int32
	status   int
	gate     chan struct{}
	lastKey  atomic.Value
	lastUA   atomic.Value
	lastPath atomic.Value
}

func (f *fakeAA) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.calls.Add(1)
	f.lastKey.Store(r.Header.Get("x-api-key"))
	f.lastUA.Store(r.Header.Get("User-Agent"))
	f.lastPath.Store(r.URL.Path)
	if f.gate != nil {
		<-f.gate
	}
	switch f.status {
	case 0, http.StatusOK:
	case http.StatusTooManyRequests:
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(f.status)
		_, _ = w.Write([]byte(`{"error":"rate limited"}`))
		return
	default:
		w.WriteHeader(f.status)
		_, _ = w.Write([]byte(`{"error":"nope"}`))
		return
	}
	if r.URL.Query().Get("page") == "2" {
		_, _ = w.Write([]byte(pageTwo))
		return
	}
	_, _ = w.Write([]byte(pageOne))
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newTestService(t *testing.T, f *fakeAA) (*Service, *clock) {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	clk := &clock{t: time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)}
	return NewService(srv.URL, clk.now), clk
}

func settings(tier string) Settings {
	return Settings{APIKey: "aa-key", KeySource: KeySourceConfig, Tier: tier, CacheTTL: time.Hour}
}

func f64(v float64) *float64 { return &v }

func TestFetchMapsFieldsAndPaginates(t *testing.T) {
	f := &fakeAA{t: t}
	svc, _ := newTestService(t, f)
	res, err := svc.Get(context.Background(), nil, settings(TierFree), false)
	if err != nil {
		t.Fatal(err)
	}
	if got := f.lastKey.Load(); got != "aa-key" {
		t.Fatalf("x-api-key = %v", got)
	}
	if got := f.lastUA.Load(); got != UserAgent {
		t.Fatalf("User-Agent = %v", got)
	}
	if got := f.lastPath.Load(); got != "/language/models/free" {
		t.Fatalf("path = %v", got)
	}
	if f.calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2 pages", f.calls.Load())
	}
	snap := res.Snapshot
	if res.Cached || snap.Tier != "free" || string(snap.IntelligenceIndexVersion) != `"4.0"` || len(snap.Models) != 3 {
		t.Fatalf("snapshot = %+v cached=%v", snap, res.Cached)
	}

	m1 := snap.Models[0]
	if m1.ID != "m1" || m1.Slug != "model-one" || m1.Creator.Name != "Acme" || m1.Creator.Country == nil || *m1.Creator.Country != "US" ||
		!m1.Reasoning || m1.OpenWeights == nil || *m1.OpenWeights || *m1.ContextWindow != 200000 || *m1.IntelligenceIndex != 61.5 ||
		*m1.CodingIndex != 55 || m1.AgenticIndex != nil || *m1.IndexCost != 812.4 || *m1.OutputTokensPerSecond != 120.5 ||
		*m1.TimeToFirstToken != 0.8 || *m1.EndToEndSeconds != 9.1 || *m1.OpenRouterID != "acme/model-one" || *m1.ReleaseDate != "2026-01-02" {
		t.Fatalf("m1 = %+v", m1)
	}
	if m1.PriceBlended == nil || *m1.PriceBlended != 2 { // (3*1 + 5) / 4
		t.Fatalf("blended fallback = %v", m1.PriceBlended)
	}

	m2 := snap.Models[1]
	if m2.ReleaseDate != nil || m2.Creator.Country != nil || m2.IntelligenceIndex != nil || m2.IndexCost != nil ||
		m2.PriceOutput != nil || *m2.PriceBlended != 3.5 || m2.OutputTokensPerSecond != nil || m2.ContextWindow != nil ||
		m2.OpenRouterID != nil || !*m2.OpenWeights {
		t.Fatalf("m2 = %+v", m2)
	}
	raw, err := json.Marshal(m2)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if v, ok := decoded["intelligence_index"]; !ok || v != nil {
		t.Fatalf("unmeasured value must serialize as null: %s", raw)
	}
	if snap.Models[2].OpenWeights != nil || snap.Models[2].PriceBlended != nil {
		t.Fatalf("m3 = %+v", snap.Models[2])
	}
}

func TestProTierUsesPaidEndpoint(t *testing.T) {
	f := &fakeAA{t: t}
	svc, _ := newTestService(t, f)
	if _, err := svc.Get(context.Background(), nil, settings(TierPro), false); err != nil {
		t.Fatal(err)
	}
	if got := f.lastPath.Load(); got != "/language/models" {
		t.Fatalf("path = %v", got)
	}
}

func TestCacheHitRefreshAndExpiry(t *testing.T) {
	f := &fakeAA{t: t}
	svc, clk := newTestService(t, f)
	ctx := context.Background()
	if _, err := svc.Get(ctx, nil, settings(TierFree), false); err != nil {
		t.Fatal(err)
	}
	res, err := svc.Get(ctx, nil, settings(TierFree), false)
	if err != nil || !res.Cached || f.calls.Load() != 2 {
		t.Fatalf("expected cache hit: cached=%v calls=%d err=%v", res.Cached, f.calls.Load(), err)
	}
	res, err = svc.Get(ctx, nil, settings(TierFree), true)
	if err != nil || res.Cached || f.calls.Load() != 4 {
		t.Fatalf("refresh must bypass cache: cached=%v calls=%d err=%v", res.Cached, f.calls.Load(), err)
	}
	clk.add(59 * time.Minute)
	if res, _ = svc.Get(ctx, nil, settings(TierFree), false); !res.Cached {
		t.Fatal("entry expired early")
	}
	clk.add(2 * time.Minute)
	if res, _ = svc.Get(ctx, nil, settings(TierFree), false); res.Cached || f.calls.Load() != 6 {
		t.Fatalf("entry did not expire: cached=%v calls=%d", res.Cached, f.calls.Load())
	}
	other := settings(TierFree)
	other.APIKey = "different"
	if res, _ = svc.Get(ctx, nil, other, false); res.Cached {
		t.Fatal("a different key must not reuse another key's cache")
	}
	svc.Clear()
	if res, _ = svc.Get(ctx, nil, settings(TierFree), false); res.Cached {
		t.Fatal("Clear did not drop the cache")
	}
}

func TestConcurrentRequestsShareOneFetch(t *testing.T) {
	f := &fakeAA{t: t, gate: make(chan struct{})}
	svc, _ := newTestService(t, f)
	const n = 8
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.Get(context.Background(), nil, settings(TierFree), true)
			errs <- err
		}()
	}
	// Wait until every other caller joined the single gated fetch.
	deadline := time.Now().Add(5 * time.Second)
	for {
		svc.mu.Lock()
		joins := svc.joins
		svc.mu.Unlock()
		if joins == n-1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d callers joined", joins)
		}
		runtime.Gosched()
	}
	close(f.gate)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := f.calls.Load(); got != 2 { // one fetch = two pages
		t.Fatalf("upstream calls = %d, want 2", got)
	}
}

func TestUnauthorizedAndRateLimit(t *testing.T) {
	f := &fakeAA{t: t, status: http.StatusUnauthorized}
	svc, _ := newTestService(t, f)
	_, err := svc.Get(context.Background(), nil, settings(TierFree), false)
	e, ok := IsUpstreamError(err)
	if !ok || !e.Unauthorized() || e.Message != "invalid or unauthorized Artificial Analysis API key" {
		t.Fatalf("err = %v", err)
	}

	f.status = http.StatusTooManyRequests
	_, err = svc.Get(context.Background(), nil, settings(TierFree), false)
	e, ok = IsUpstreamError(err)
	if !ok || !e.RateLimited() || e.RetryAfter != 3600 {
		t.Fatalf("err = %v", err)
	}

	// With earlier data, a rate-limited refresh serves that data with a warning.
	f.status = http.StatusOK
	if _, err = svc.Get(context.Background(), nil, settings(TierFree), false); err != nil {
		t.Fatal(err)
	}
	f.status = http.StatusTooManyRequests
	res, err := svc.Get(context.Background(), nil, settings(TierFree), true)
	if err != nil || !res.Cached || res.Warning == "" || res.RetryAfter != 3600 || len(res.Snapshot.Models) != 3 {
		t.Fatalf("stale serve: res=%+v err=%v", res, err)
	}
}

func TestResolveSettings(t *testing.T) {
	env := func(v string) func(string) (string, bool) {
		return func(key string) (string, bool) {
			if key == EnvAPIKey && v != "" {
				return v, true
			}
			return "", false
		}
	}
	s := resolveSettings(config.ModelIntelligenceConfig{}, env(""))
	if s.Configured() || s.KeySource != "" || s.Tier != TierFree || s.CacheTTL != DefaultCacheTTL {
		t.Fatalf("defaults = %+v", s)
	}
	s = resolveSettings(config.ModelIntelligenceConfig{CacheTTL: "bogus", ArtificialAnalysisTier: "PRO"}, env("from-env"))
	if s.APIKey != "from-env" || s.KeySource != KeySourceEnv || s.Tier != TierPro || s.CacheTTL != DefaultCacheTTL {
		t.Fatalf("env = %+v", s)
	}
	s = resolveSettings(config.ModelIntelligenceConfig{ArtificialAnalysisAPIKey: " cfg ", CacheTTL: "30m"}, env("from-env"))
	if s.APIKey != "cfg" || s.KeySource != KeySourceConfig || s.CacheTTL != 30*time.Minute {
		t.Fatalf("config = %+v", s)
	}
}
