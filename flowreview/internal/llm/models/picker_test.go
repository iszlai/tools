package models

import "testing"

func TestPickerData(t *testing.T) {
	large := PickerData(32 * gb)
	if large.Tier != TierLarge {
		t.Errorf("Tier = %q, want %q", large.Tier, TierLarge)
	}
	if large.OffByDefault || large.Reason != "" {
		t.Errorf("32 GB should not be off by default, got OffByDefault=%v Reason=%q", large.OffByDefault, large.Reason)
	}
	if len(large.Entries) == 0 || !large.Entries[0].Recommended {
		t.Errorf("32 GB: the recommended model should be listed first, got %+v", large.Entries)
	}
	medium := PickerData(16 * gb)
	if len(medium.Entries) == 0 || medium.Entries[0].Name != "qwen3.5-4b-q4_k_m" {
		t.Errorf("16 GB: want qwen3.5-4b-q4_k_m first, got %+v", medium.Entries)
	}

	small := PickerData(8 * gb)
	if small.Tier != TierSmall {
		t.Errorf("Tier = %q, want %q", small.Tier, TierSmall)
	}
	if !small.OffByDefault || small.Reason == "" {
		t.Error("8 GB should be off by default with a reason")
	}
	for _, e := range small.Entries {
		if e.Recommended {
			t.Errorf("8 GB: %s is recommended but needs %d GB", e.Name, e.MinRAMGB)
		}
	}
	if len(small.Entries) == 0 {
		t.Error("summaries being off by default should not hide the small tier from the picker")
	}
}
