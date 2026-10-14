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
	for _, e := range large.Entries {
		if e.Recommended {
			t.Errorf("%s: Recommended before FR-27 ran", e.Name)
		}
	}

	small := PickerData(8 * gb)
	if small.Tier != TierSmall {
		t.Errorf("Tier = %q, want %q", small.Tier, TierSmall)
	}
	if !small.OffByDefault || small.Reason == "" {
		t.Error("8 GB should be off by default with a reason")
	}
	if len(small.Entries) == 0 {
		t.Error("summaries being off by default should not hide the small tier from the picker")
	}
}
