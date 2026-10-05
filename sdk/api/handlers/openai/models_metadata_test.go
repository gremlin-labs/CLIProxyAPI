package openai

import (
	"reflect"
	"testing"
)

func TestOpenAIModelCapabilityMetadataUsesCatalog(t *testing.T) {
	got := openAIModelCapabilityMetadata(map[string]any{"id": "claude-opus-4-6"})
	if got["context_window"] != 1000000 || got["max_context_window"] != 1000000 {
		t.Fatalf("context window = %v/%v, want 1000000", got["context_window"], got["max_context_window"])
	}
	wantLevels := []string{"low", "medium", "high", "max"}
	if !reflect.DeepEqual(got["supported_reasoning_levels"], wantLevels) {
		t.Fatalf("supported_reasoning_levels = %v, want %v", got["supported_reasoning_levels"], wantLevels)
	}
	if got["default_reasoning_level"] != "medium" {
		t.Fatalf("default_reasoning_level = %v, want medium", got["default_reasoning_level"])
	}
}

func TestOpenAIModelCapabilityMetadataOmitsUnknownValues(t *testing.T) {
	// Budget-only thinking (no named levels) must not invent a level list.
	budgetOnly := openAIModelCapabilityMetadata(map[string]any{"id": "claude-haiku-4-5-20251001"})
	if _, ok := budgetOnly["supported_reasoning_levels"]; ok {
		t.Fatalf("budget-only model got reasoning levels: %v", budgetOnly)
	}
	if budgetOnly["context_window"] != 200000 {
		t.Fatalf("context_window = %v, want 200000", budgetOnly["context_window"])
	}

	if got := openAIModelCapabilityMetadata(map[string]any{"id": "no-such-model-xyz"}); len(got) != 0 {
		t.Fatalf("unknown model metadata = %v, want none", got)
	}
}

func TestOpenAIModelCapabilityMetadataPrefersConfiguredContext(t *testing.T) {
	got := openAIModelCapabilityMetadata(map[string]any{
		"id":                 "claude-opus-4-6",
		"context_length":     200000,
		"max_context_length": 400000,
	})
	if got["context_window"] != 400000 {
		t.Fatalf("context_window = %v, want the configured 400000 override", got["context_window"])
	}
}

func TestReasoningLevelsDefault(t *testing.T) {
	cases := []struct {
		raw         []string
		wantDefault string
	}{
		{[]string{"low", "medium", "high"}, "medium"},
		{[]string{"none", "low", "high"}, "low"},
		{[]string{"none"}, "none"},
		{[]string{" High ", "high", "LOW"}, "high"},
	}
	for _, tc := range cases {
		if _, got := reasoningLevels(tc.raw); got != tc.wantDefault {
			t.Fatalf("reasoningLevels(%v) default = %q, want %q", tc.raw, got, tc.wantDefault)
		}
	}
	if levels, _ := reasoningLevels([]string{" High ", "high", "LOW"}); !reflect.DeepEqual(levels, []string{"high", "low"}) {
		t.Fatalf("levels = %v, want deduplicated lowercase [high low]", levels)
	}
}
