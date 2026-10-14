package models

import "testing"

// TestCatalogComplete asserts every shipped entry carries what Install and
// the UI disclosure need, and that exactly one entry is Recommended.
func TestCatalogComplete(t *testing.T) {
	if len(Catalog) == 0 {
		t.Fatal("catalog is empty")
	}
	seen := map[string]bool{}
	recommended := 0
	for _, e := range Catalog {
		if e.Name == "" {
			t.Fatalf("entry with empty Name: %+v", e)
		}
		if seen[e.Name] {
			t.Fatalf("duplicate catalog name %q", e.Name)
		}
		seen[e.Name] = true

		if e.URL == "" {
			t.Errorf("%s: missing URL", e.Name)
		}
		if len(e.SHA256) != 64 {
			t.Errorf("%s: SHA256 is not a 64-char hex digest: %q", e.Name, e.SHA256)
		}
		if e.Size <= 0 {
			t.Errorf("%s: Size must be positive, got %d", e.Name, e.Size)
		}
		if e.Licence == "" {
			t.Errorf("%s: missing Licence", e.Name)
		}
		if e.LicenceURL == "" {
			t.Errorf("%s: missing LicenceURL", e.Name)
		}
		if e.Params == "" {
			t.Errorf("%s: missing Params", e.Name)
		}
		if e.MinRAMGB <= 0 {
			t.Errorf("%s: MinRAMGB must be positive, got %d", e.Name, e.MinRAMGB)
		}
		switch e.Tier {
		case TierLarge, TierMedium, TierSmall:
		default:
			t.Errorf("%s: unknown Tier %q", e.Name, e.Tier)
		}
		if e.Recommended {
			recommended++
		}
	}
	if recommended != 1 {
		t.Errorf("%d Recommended entries, want exactly 1", recommended)
	}
}

func TestLookup(t *testing.T) {
	want := Catalog[0]
	got, ok := Lookup(want.Name)
	if !ok || got != want {
		t.Fatalf("Lookup(%q) = %+v, %v; want %+v, true", want.Name, got, ok, want)
	}
	if _, ok := Lookup("does-not-exist"); ok {
		t.Error("Lookup of an unknown name reported ok")
	}
}

func TestFilter(t *testing.T) {
	for _, tier := range []string{TierLarge, TierMedium, TierSmall} {
		entries := Filter(tier)
		if len(entries) == 0 {
			t.Errorf("Filter(%q) returned nothing", tier)
		}
		for _, e := range entries {
			if e.Tier != tier {
				t.Errorf("Filter(%q) returned %s with Tier %q", tier, e.Name, e.Tier)
			}
		}
	}
}

func TestTierFor(t *testing.T) {
	cases := []struct {
		gb   uint64
		want string
	}{
		{64, TierLarge},
		{24, TierLarge},
		{23, TierMedium},
		{16, TierMedium},
		{15, TierSmall},
		{8, TierSmall},
		{4, TierSmall},
	}
	for _, c := range cases {
		if got := TierFor(c.gb * gb); got != c.want {
			t.Errorf("TierFor(%d GB) = %q, want %q", c.gb, got, c.want)
		}
	}
}

func TestSummariesOffByDefault(t *testing.T) {
	if !SummariesOffByDefault(8 * gb) {
		t.Error("8 GB should default summaries off")
	}
	if !SummariesOffByDefault(15 * gb) {
		t.Error("15 GB should default summaries off")
	}
	if SummariesOffByDefault(16 * gb) {
		t.Error("16 GB should not default summaries off")
	}
	if SummariesOffByDefault(32 * gb) {
		t.Error("32 GB should not default summaries off")
	}
	if reason := SummariesOffReason(8 * gb); reason == "" {
		t.Error("SummariesOffReason returned an empty string")
	}
}
