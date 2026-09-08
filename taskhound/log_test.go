package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// messyLog is the shape a log that predates the tool actually has, and every
// awkward line in it is copied from a real one: a preamble closed by a rule,
// separators that stop halfway down, a title wrapped onto a second ## line, and
// an entry whose heading carries no date at all.
const messyLog = `# Captain's log

Append-only. Newest entry at the bottom.

---

## 2026-08-22 — Engine install

Installed the engine, after asking. Amended the setup notes while I was there.

**Superseded within the hour** — see the entry below.

---

## 2026-08-22 — Adopting the workflow

Mirror the neighbouring project rather than invent a second way.

**Amended:** ` + "`vision.md`" + ` §4.1, which said the opposite.

## 2026-08-23 — The firing step exists because towers cannot shoot over
## themselves

The bastion's own parapet is in the way. TH-3 covers the fix.

## WO-8a — the threat overlay, corridor tint

A count, not the number the map already kept. TH-1 measured it.
`

func writeLog(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), LogName)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readLog(t *testing.T, path string) *Log {
	t.Helper()
	l, err := ReadLog(path)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// TestTheParserReadsTheLogItInherited pins the shapes the writer would never
// produce and the reader has to accept anyway, because the file is append-only
// and cannot be tidied to suit its own tool.
func TestTheParserReadsTheLogItInherited(t *testing.T) {
	l := readLog(t, writeLog(t, messyLog))

	if len(l.Entries) != 4 {
		var got []string
		for _, e := range l.Entries {
			got = append(got, e.Title)
		}
		t.Fatalf("read %d entries, want 4: %v", len(l.Entries), got)
	}
	if want := "# Captain's log"; len(l.Preamble) == 0 || l.Preamble[0] != want {
		t.Errorf("the header above the first entry was not kept: %v", l.Preamble)
	}
	if last := l.Preamble[len(l.Preamble)-1]; strings.TrimSpace(last) == "---" {
		t.Error("the rule closing the header was read as part of it")
	}

	// The wrapped title is one entry, not two, and reads as one sentence.
	wrapped := l.Entries[2]
	if want := "The firing step exists because towers cannot shoot over themselves"; wrapped.Title != want {
		t.Errorf("wrapped title = %q, want %q", wrapped.Title, want)
	}
	if len(wrapped.heading) != 2 {
		t.Errorf("the wrapped heading should be kept as written, both lines: %v", wrapped.heading)
	}

	// An undated heading is still an entry, and it belongs to the date of the
	// entry above it -- that is where it sits in the file.
	undated := l.Entries[3]
	if undated.Date != "" {
		t.Errorf("date = %q, want none", undated.Date)
	}
	if undated.Effective != "2026-08-23" {
		t.Errorf("effective date = %q, want the dated entry above it", undated.Effective)
	}
	if !strings.Contains(strings.Join(undated.Body, "\n"), "A count, not the number") {
		t.Errorf("the undated entry lost its body: %v", undated.Body)
	}

	// A --- between two entries is punctuation and not the last line of the one
	// above, or every entry printed back would trail a rule.
	for _, e := range l.Entries {
		if n := len(e.Body); n > 0 && strings.TrimSpace(e.Body[n-1]) == "---" {
			t.Errorf("%q kept the separator below it", e.Title)
		}
	}
}

// TestAddWritesTheFormatRatherThanTheFilesHabit is the decision this file is
// built on: there is one entry format, th log writes it, and it does not matter
// what the entry above happened to look like.
func TestAddWritesTheFormatRatherThanTheFilesHabit(t *testing.T) {
	path := writeLog(t, messyLog)
	l := readLog(t, path)

	heading := l.Heading("2026-09-09", "A new decision", false)
	if want := "## 2026-09-09 — A new decision"; heading != want {
		t.Fatalf("heading = %q, want %q", heading, want)
	}
	if err := l.Append(heading, "The body.\n"); err != nil {
		t.Fatal(err)
	}

	again := readLog(t, path)
	if len(again.Entries) != 5 {
		t.Fatalf("appending gave %d entries, want 5", len(again.Entries))
	}
	added := again.Entries[4]
	if added.Title != "A new decision" || added.Date != "2026-09-09" {
		t.Errorf("read the new entry back as %q on %q", added.Title, added.Date)
	}
	if !added.Canonical() {
		t.Error("th log wrote an entry it does not consider canonical")
	}
	// The entries either side of it in the file used --- separators. The new one
	// does not, because the format says so.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tail := string(raw[strings.Index(string(raw), "## 2026-09-09"):])
	if strings.Contains(tail, "---") {
		t.Errorf("the new entry brought a separator with it:\n%s", tail)
	}
	if !strings.HasPrefix(tail, "## 2026-09-09 — A new decision\n\nThe body.\n") {
		t.Errorf("the appended entry is not heading, blank line, prose:\n%q", tail)
	}
}

// Nothing above the appended entry may move: the file is append-only, and a
// reader with no th at all has to see exactly what it saw before plus one entry.
func TestAppendingChangesNothingAboveIt(t *testing.T) {
	path := writeLog(t, messyLog)
	l := readLog(t, path)
	if err := l.Append(l.Heading("2026-09-09", "Another", false), "Body."); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(raw), messyLog) {
		t.Error("appending rewrote something above the new entry")
	}
}

// The format was chosen to be the one the log already mostly uses, so adopting
// it leaves check quiet about the history rather than red down every line. The
// two ordinary entries match it; the wrapped title and the undated heading are
// the only two that do not.
func TestTheFormatIsTheOneTheLogAlreadyMostlyUses(t *testing.T) {
	l := readLog(t, writeLog(t, messyLog))
	want := map[string]bool{
		"Engine install":        true,
		"Adopting the workflow": true,
		"The firing step exists because towers cannot shoot over themselves": false,
		"WO-8a — the threat overlay, corridor tint":                          false,
	}
	for _, e := range l.Entries {
		expected, known := want[e.Title]
		if !known {
			t.Fatalf("the fixture grew an entry the test does not know: %q", e.Title)
		}
		if e.Canonical() != expected {
			t.Errorf("%q: canonical = %v, want %v", e.Title, e.Canonical(), expected)
		}
	}
}

func TestAnEmptyLogTakesTheFirstEntryCleanly(t *testing.T) {
	path := filepath.Join(t.TempDir(), LogName)
	l, err := ReadLog(path)
	if !os.IsNotExist(err) {
		t.Fatalf("reading a log that is not there gave %v", err)
	}
	l = ParseLog(nil)
	l.Path = path
	if err := l.Append(l.Heading("2026-09-09", "First", false), "Body."); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "## 2026-09-09 — First\n\nBody.\n"; string(raw) != want {
		t.Errorf("first entry wrote %q, want %q", raw, want)
	}
}

func TestTailAndSince(t *testing.T) {
	l := readLog(t, writeLog(t, messyLog))
	if got := l.Tail(2); len(got) != 2 || got[1].Date != "" {
		t.Errorf("tail 2 did not give the last two entries: %v", got)
	}
	if got := l.Tail(99); len(got) != 4 {
		t.Errorf("tail past the start should give everything, got %d", len(got))
	}
	// The undated entry sits after 2026-08-23 and comes with it.
	since := l.Since("2026-08-23")
	if len(since) != 2 {
		t.Fatalf("since 2026-08-23 gave %d entries, want 2", len(since))
	}
	if since[1].Date != "" {
		t.Error("an undated entry was dropped from --since rather than sitting where it is")
	}
}

func TestIDsAreFoundByPrefixAndNotByGuesswork(t *testing.T) {
	l := readLog(t, writeLog(t, messyLog+
		"\n## 2026-09-09 — TH-9: encoding notes\n\nThe file is UTF-8 and AES-256 is not an id.\n"))
	ids := l.IDs()
	if _, ok := ids["TH"]["TH-9"]; !ok {
		t.Error("an id in a heading was not found")
	}
	if _, ok := ids["TH"]["TH-3"]; !ok {
		t.Error("an id in the prose was not found")
	}
	// A hand-subdivided id is a real reference and has to be visible.
	if _, ok := ids["WO"]["WO-8a"]; !ok {
		t.Errorf("a suffixed id was missed: %v", ids["WO"])
	}
	// UTF-8 and AES-256 are id-shaped and are not ids, which is exactly why the
	// caller only ever checks the prefix its own board uses.
	if _, ok := ids["UTF"]; !ok {
		t.Error("the pattern is narrower than documented; the prefix filter is what makes it safe")
	}
}

func TestIssueOffersTheEntriesThatAreAboutItFirst(t *testing.T) {
	l := readLog(t, writeLog(t,
		"## 2026-09-01 — A decision that touched it in passing\n\nProse naming TH-9.\n"+
			"\n## 2026-09-02 — TH-9: the decision itself\n\nBody.\n"))
	re := mustPattern(t, `\bTH-9\b`)
	matches := searchLog(l.Entries, re)
	if len(matches) != 2 {
		t.Fatalf("found %d entries naming TH-9, want 2", len(matches))
	}
	sortHeadingsFirst(matches)
	if !matchInHeading(matches[0]) {
		t.Error("the entry TH-9 is about did not come first")
	}
	if matchInHeading(matches[1]) {
		t.Error("the passing mention was reported as being about TH-9")
	}
}

func TestGrepGroupsHitsUnderTheDecisionTheyBelongTo(t *testing.T) {
	l := readLog(t, writeLog(t, messyLog))
	matches := searchLog(l.Entries, mustPattern(t, `(?i)parapet`))
	if len(matches) != 1 {
		t.Fatalf("matched %d entries, want 1", len(matches))
	}
	if !strings.HasPrefix(matches[0].Entry.Title, "The firing step") {
		t.Errorf("the hit was filed under %q", matches[0].Entry.Title)
	}
	if len(matches[0].Hits) != 1 || matches[0].Hits[0].Line == 0 {
		t.Errorf("a hit has to carry its line number in the file: %v", matches[0].Hits)
	}
}

func TestAmendmentsSeeTheConventionAndSayWhatTheyCannotSee(t *testing.T) {
	l := readLog(t, writeLog(t, messyLog))
	marked, loose := 0, 0
	for _, e := range l.Entries {
		if len(amendmentLines(e)) > 0 {
			marked++
		} else if mentionsAmendment(e) {
			loose++
		}
	}
	if marked != 1 {
		t.Errorf("found %d entries with an **Amended:** line, want 1", marked)
	}
	if loose != 1 {
		t.Errorf("found %d entries that mention an amendment without the marker, want 1", loose)
	}
}

func mustPattern(t *testing.T, pattern string) *regexp.Regexp {
	t.Helper()
	re, err := logPattern(pattern, false, true)
	if err != nil {
		t.Fatal(err)
	}
	return re
}

// TestAPaddedIDIsTheSameIssueAsAnUnpaddedOne pins the case that made check
// useless on a real log: the log writes V6-01, the board writes V6-1, and
// comparing the spelling reported 124 finished issues as unlogged.
func TestAPaddedIDIsTheSameIssueAsAnUnpaddedOne(t *testing.T) {
	for _, c := range []struct{ id, want string }{
		{"V6-01", "V6-1"},
		{"V6-1", "V6-1"},
		{"v6-001", "V6-1"},
		{"V5-00", "V5-0"},
		{"WO-8a", "WO-8a"},
		{"WO-08A", "WO-8a"},
		{"UTF-8", "UTF-8"},
		{"not-an-id", "NOT-AN-ID"},
	} {
		if got := logIDKey(c.id); got != c.want {
			t.Errorf("logIDKey(%q) = %q, want %q", c.id, got, c.want)
		}
	}

	// And the search goes both ways, because either spelling is the same issue.
	l := readLog(t, writeLog(t,
		"## 2026-09-01 — TH-01: written padded\n\nBody.\n"+
			"\n## 2026-09-02 — TH-2: written plain\n\nProse naming TH-1 and TH-002.\n"))
	for _, ref := range []string{"TH-1", "TH-01", "TH-0001"} {
		re, err := logIDRegexp(ref)
		if err != nil {
			t.Fatal(err)
		}
		if n := len(searchLog(l.Entries, re)); n != 2 {
			t.Errorf("%s matched %d entries, want both spellings (2)", ref, n)
		}
	}
	// Tolerance stops at the number: TH-1 is not TH-2 and not TH-12.
	re, err := logIDRegexp("TH-2")
	if err != nil {
		t.Fatal(err)
	}
	matches := searchLog(l.Entries, re)
	if len(matches) != 1 || !strings.Contains(matches[0].Entry.Title, "written plain") {
		t.Errorf("TH-2 matched %d entries, want only its own", len(matches))
	}
}
