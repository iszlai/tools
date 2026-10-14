package models

// Picker is what the UI's first-run model picker renders: the catalog
// filtered to the machine's RAM tier, plus whether summaries should default
// to off and why. It is JSON-serializable so the web UI can fetch it as-is.
// There is no preselected entry — PLAN.md §8 is explicit that the user picks.
type Picker struct {
	Tier         string  `json:"tier"`
	Entries      []Entry `json:"entries"`
	OffByDefault bool    `json:"off_by_default"`
	Reason       string  `json:"reason,omitempty"`
}

// PickerData builds the first-run picker for a machine with ramBytes of
// physical RAM.
func PickerData(ramBytes uint64) Picker {
	tier := TierFor(ramBytes)
	p := Picker{
		Tier:         tier,
		Entries:      Filter(tier),
		OffByDefault: SummariesOffByDefault(ramBytes),
	}
	if p.OffByDefault {
		p.Reason = SummariesOffReason(ramBytes)
	}
	return p
}
