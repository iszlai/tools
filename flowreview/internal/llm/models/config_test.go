package models

import (
	"testing"

	"github.com/iszlai/tools/flowreview/internal/model"
)

func TestConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()

	cfg, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig with no file: %v", err)
	}
	if cfg.Enabled {
		t.Error("a missing config should mean summaries off (Enabled == false)")
	}

	want := model.ModelConfig{Enabled: true, Model: "qwen2.5-coder-7b-instruct-q4_k_m", Tier: TierLarge}
	if err := SaveConfig(dir, want); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	got, err := LoadConfig(dir)
	if err != nil {
		t.Fatalf("LoadConfig after save: %v", err)
	}
	if got != want {
		t.Errorf("LoadConfig() = %+v, want %+v", got, want)
	}
}
