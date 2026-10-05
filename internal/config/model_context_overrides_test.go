package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestModelContextOverridesLoadFromV8RoutingAndSaveBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	raw := "config-version: 8\nrouting:\n  model-context-overrides:\n    - model: Custom-A\n      context-length: 262144\n    - model: custom-a\n      context-length: 131072\n      max-completion-tokens: 8192\n    - model: empty\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	// Duplicates collapse case-insensitively (last wins) and empty entries drop.
	if len(cfg.ModelContextOverrides) != 1 {
		t.Fatalf("overrides = %+v, want one entry", cfg.ModelContextOverrides)
	}
	got := cfg.ModelContextOverrides[0]
	if got.Model != "custom-a" || got.ContextLength != 131072 || got.MaxCompletionTokens != 8192 {
		t.Fatalf("override = %+v", got)
	}

	cfg.ModelContextOverrides = append(cfg.ModelContextOverrides, ModelContextOverride{Model: "custom-b", ContextLength: 65536})
	if errSave := SaveConfigPreserveComments(path, cfg, true); errSave != nil {
		t.Fatal(errSave)
	}
	saved, _ := os.ReadFile(path)
	var doc yaml.Node
	if errParse := yaml.Unmarshal(saved, &doc); errParse != nil {
		t.Fatal(errParse)
	}
	node := yamlPath(doc.Content[0], "routing.model-context-overrides")
	if node == nil || len(node.Content) != 2 {
		t.Fatalf("routing.model-context-overrides not saved as two entries:\n%s", saved)
	}
	if yamlPath(doc.Content[0], "model-context-overrides") != nil {
		t.Fatalf("legacy top-level key written in v8 layout:\n%s", saved)
	}
	if !strings.Contains(string(saved), "custom-b") {
		t.Fatalf("new override missing:\n%s", saved)
	}
}
