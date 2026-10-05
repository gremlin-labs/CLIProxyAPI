package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestModelIntelligenceV8RoundTrip checks the v8 observability.model-intelligence
// mapping, that saves keep the v8 layout, and that a cleared key leaves the file.
func TestModelIntelligenceV8RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	raw := "config-version: 8\nobservability:\n  model-intelligence:\n    artificial-analysis-api-key: aa-secret\n    artificial-analysis-tier: pro\n    cache-ttl: 2h\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	mi := cfg.ModelIntelligence
	if mi.ArtificialAnalysisAPIKey != "aa-secret" || mi.ArtificialAnalysisTier != "pro" || mi.CacheTTL != "2h" {
		t.Fatalf("loaded model intelligence = %+v", mi)
	}

	cfg.ModelIntelligence.CacheTTL = "3h"
	if errSave := SaveConfigPreserveComments(path, cfg, true); errSave != nil {
		t.Fatal(errSave)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(saved)
	if !strings.Contains(text, "cache-ttl: 3h") || !strings.Contains(text, "artificial-analysis-api-key: aa-secret") {
		t.Fatalf("v8 save lost model-intelligence values:\n%s", text)
	}
	if strings.Contains(text, "\nmodel-intelligence:") || !strings.Contains(text, "  model-intelligence:") {
		t.Fatalf("v8 save did not keep observability.model-intelligence:\n%s", text)
	}

	cfg.ModelIntelligence.ArtificialAnalysisAPIKey = ""
	if errSave := SaveConfigPreserveComments(path, cfg, true); errSave != nil {
		t.Fatal(errSave)
	}
	saved, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), "artificial-analysis-api-key") {
		t.Fatalf("cleared key survived the save:\n%s", saved)
	}
	reloaded, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.ModelIntelligence; got.ArtificialAnalysisAPIKey != "" || got.ArtificialAnalysisTier != "pro" || got.CacheTTL != "3h" {
		t.Fatalf("round trip = %+v", got)
	}
}

// TestModelIntelligenceLegacyRoundTrip checks the top-level legacy spelling and
// that an unset section is never written.
func TestModelIntelligenceLegacyRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("port: 8317\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if errSave := SaveConfigPreserveComments(path, cfg); errSave != nil {
		t.Fatal(errSave)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), "model-intelligence") {
		t.Fatalf("empty section was written:\n%s", saved)
	}

	cfg.ModelIntelligence.ArtificialAnalysisAPIKey = "aa-legacy"
	if errSave := SaveConfigPreserveComments(path, cfg); errSave != nil {
		t.Fatal(errSave)
	}
	saved, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), "model-intelligence:\n  artificial-analysis-api-key: aa-legacy") {
		t.Fatalf("legacy save did not write the top-level section:\n%s", saved)
	}
	cfg.ModelIntelligence.ArtificialAnalysisAPIKey = ""
	if errSave := SaveConfigPreserveComments(path, cfg); errSave != nil {
		t.Fatal(errSave)
	}
	saved, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), "artificial-analysis-api-key") {
		t.Fatalf("cleared legacy key survived the save:\n%s", saved)
	}
}
