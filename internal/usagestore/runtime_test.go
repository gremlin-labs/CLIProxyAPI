package usagestore

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	coreusage "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/usage"
)

func TestResolveStorePath(t *testing.T) {
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "config.yaml")
	absolute := filepath.Join(t.TempDir(), "custom.db")

	t.Setenv("WRITABLE_PATH", "")
	t.Setenv("writable_path", "")
	if got, want := ResolveStorePath("", configPath), filepath.Join(configDir, DefaultStoreRelativePath); got != want {
		t.Fatalf("default path = %q, want %q", got, want)
	}
	if got, want := ResolveStorePath("data/usage.db", configPath), filepath.Join(configDir, "data", "usage.db"); got != want {
		t.Fatalf("relative path = %q, want %q", got, want)
	}
	if got := ResolveStorePath(absolute, configPath); got != absolute {
		t.Fatalf("absolute path = %q, want %q", got, absolute)
	}

	writable := t.TempDir()
	t.Setenv("WRITABLE_PATH", writable)
	if got, want := ResolveStorePath("", configPath), filepath.Join(writable, DefaultStoreRelativePath); got != want {
		t.Fatalf("writable path = %q, want %q", got, want)
	}
	if got := ResolveStorePath(absolute, configPath); got != absolute {
		t.Fatalf("absolute path with WRITABLE_PATH = %q, want %q", got, absolute)
	}
}

// TestPluginPersistsUsageRecord covers the recording path: a usage record
// delivered to the plugin is enqueued and flushed to SQLite on Close.
func TestPluginPersistsUsageRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.db")
	store, err := Open(Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	plugin := NewPlugin(store)
	requestedAt := time.UnixMilli(1_700_000_000_000)
	plugin.HandleUsage(context.Background(), coreusage.Record{
		Provider:     "claude",
		ExecutorType: "ClaudeExecutor",
		Model:        "claude-sonnet-4-5",
		APIKey:       "client-key",
		Source:       "user@example.com",
		AuthIndex:    "auth-1",
		RequestedAt:  requestedAt,
		Latency:      1500 * time.Millisecond,
		Detail:       coreusage.Detail{InputTokens: 100, OutputTokens: 20, CacheReadTokens: 400},
	})
	plugin.HandleUsage(context.Background(), coreusage.Record{
		Provider:    "codex",
		Model:       "gpt-5-codex",
		RequestedAt: requestedAt.Add(time.Second),
		Failed:      true,
		Fail:        coreusage.Failure{StatusCode: http.StatusTooManyRequests, Body: "rate\nlimited"},
	})
	plugin.SetEnabled(false)
	plugin.HandleUsage(context.Background(), coreusage.Record{Model: "ignored", RequestedAt: requestedAt})
	if errClose := store.Close(); errClose != nil {
		t.Fatal(errClose)
	}

	reopened, err := Open(Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if errClose := reopened.Close(); errClose != nil {
			t.Errorf("close: %v", errClose)
		}
	}()
	events, err := reopened.ListEvents(context.Background(), QueryFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %+v, want 2 (disabled plugin must not record)", events)
	}
	failed, ok := events[0], events[1]
	if !failed.Failed || failed.FailStatusCode != http.StatusTooManyRequests || failed.FailSummary != "rate limited" || failed.Alias != "gpt-5-codex" {
		t.Fatalf("failed event = %+v", failed)
	}
	if ok.Failed || ok.FailStatusCode != http.StatusOK || ok.TimestampMS != requestedAt.UnixMilli() ||
		ok.APIKeyHash != HashSecret("client-key") || ok.TotalTokens != 120 || ok.CacheReadTokens != 400 ||
		ok.LatencyMS == nil || *ok.LatencyMS != 1500 {
		t.Fatalf("success event = %+v", ok)
	}
}

// TestPriceSyncSendsHonestUserAgent asserts every catalog request identifies
// this proxy instead of impersonating a browser or another client.
func TestPriceSyncSendsHonestUserAgent(t *testing.T) {
	var mu sync.Mutex
	agents := map[string]string{}
	fixture := priceSyncFixtureClient(t, map[string]string{})
	base := fixture.Transport
	client := &http.Client{Transport: priceSyncFixtureTransport(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		agents[r.URL.String()] = r.Header.Get("User-Agent")
		mu.Unlock()
		return base.RoundTrip(r)
	})}
	store, err := Open(Options{Path: filepath.Join(t.TempDir(), "usage.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if errClose := store.Close(); errClose != nil {
			t.Errorf("close: %v", errClose)
		}
	}()
	_, errSync := store.syncModelPrices(context.Background(), PriceSyncRequest{Models: []string{"gpt-test"}}, client)
	if errSync == nil {
		t.Fatal("expected all-sources-failed error from the outage fixture")
	}
	for _, url := range []string{defaultModelsDevURL, defaultLiteLLMURL, defaultOpenRouterURL} {
		if got := agents[url]; got != priceSyncUserAgent {
			t.Fatalf("User-Agent for %s = %q, want %q", url, got, priceSyncUserAgent)
		}
	}
}

func TestConfigureReusesStoreAndTogglesRecording(t *testing.T) {
	t.Cleanup(CloseRuntime)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	t.Setenv("WRITABLE_PATH", "")
	t.Setenv("writable_path", "")

	first, err := Configure("", configPath, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if first.Path() != filepath.Join(filepath.Dir(configPath), DefaultStoreRelativePath) || first.RetentionDays() != DefaultRetentionDays {
		t.Fatalf("store path=%q retention=%d", first.Path(), first.RetentionDays())
	}
	if runtimePlugin.Enabled() {
		t.Fatal("plugin enabled while usage statistics are disabled")
	}
	second, err := Configure("", configPath, 7, true)
	if err != nil {
		t.Fatal(err)
	}
	if second != first || second.RetentionDays() != 7 || !runtimePlugin.Enabled() {
		t.Fatalf("reconfigure did not reuse store or apply settings: same=%v retention=%d", second == first, second.RetentionDays())
	}
	if unanchored, errUnanchored := Configure("usage.db", "", 7, true); errUnanchored != nil || unanchored != nil {
		t.Fatalf("unanchored store = %v, %v; want nil, nil", unanchored, errUnanchored)
	}
	if errWrite := os.WriteFile(configPath, []byte("port: 1\n"), 0o600); errWrite != nil {
		t.Fatal(errWrite)
	}
	// A path below a regular file cannot be created; the error must surface.
	if _, errBad := Configure(filepath.Join(configPath, "usage.db"), configPath, 7, true); errBad == nil {
		t.Fatal("expected error for an unusable store path")
	}
}
