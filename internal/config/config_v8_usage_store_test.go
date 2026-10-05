package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestV8UsageStoreKeys checks that the request monitoring store settings map to
// observability.usage in the v8 layout and survive a save round trip.
func TestV8UsageStoreKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	raw := "config-version: 8\nobservability:\n  usage:\n    usage-statistics-enabled: true\n    store-path: custom-usage.db\n    retention-days: 37\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.UsageStatisticsEnabled || cfg.UsageStorePath != "custom-usage.db" || cfg.UsageRetentionDays != 37 {
		t.Fatalf("loaded usage settings: enabled=%v path=%q retention=%d", cfg.UsageStatisticsEnabled, cfg.UsageStorePath, cfg.UsageRetentionDays)
	}
	cfg.UsageRetentionDays = 14
	if errSave := SaveConfigPreserveComments(path, cfg, true); errSave != nil {
		t.Fatal(errSave)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), "retention-days: 14") || strings.Contains(string(saved), "usage-retention-days") {
		t.Fatalf("v8 save did not keep the observability.usage layout:\n%s", saved)
	}
	reloaded, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.UsageRetentionDays != 14 || reloaded.UsageStorePath != "custom-usage.db" {
		t.Fatalf("round trip: path=%q retention=%d", reloaded.UsageStorePath, reloaded.UsageRetentionDays)
	}
}
