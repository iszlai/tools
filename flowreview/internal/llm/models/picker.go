package models

// Picker is what the UI's first-run model picker renders: the catalog
// filtered to the machine's RAM tier, plus whether summaries should default
// to off and why. It is JSON-serializable so the web UI can fetch it as-is.
// The recommended entry, when it fits the machine, is listed first (even if
// it belongs to a lower tier) and marked; the user still picks.
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
		Entries:      pickerEntries(tier, ramBytes),
		OffByDefault: SummariesOffByDefault(ramBytes),
	}
	if p.OffByDefault {
		p.Reason = SummariesOffReason(ramBytes)
	}
	return p
}

// pickerEntries is Filter(tier) with RecommendedFor(ramBytes) moved, or
// added, to the front.
func pickerEntries(tier string, ramBytes uint64) []Entry {
	rec, ok := RecommendedFor(ramBytes)
	if !ok {
		return Filter(tier)
	}
	out := []Entry{rec}
	for _, e := range Filter(tier) {
		if e.Name != rec.Name {
			out = append(out, e)
		}
	}
	return out
}
