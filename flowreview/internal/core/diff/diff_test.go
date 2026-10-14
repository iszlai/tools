package diff

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/iszlai/tools/flowreview/internal/model"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func parseOne(t *testing.T, name string) model.ChangedFile {
	t.Helper()
	files, err := Parse(readFixture(t, name))
	if err != nil {
		t.Fatalf("Parse(%s): %v", name, err)
	}
	if len(files) != 1 {
		t.Fatalf("Parse(%s): got %d files, want 1", name, len(files))
	}
	return files[0]
}

// TestRoundTrip is the slice's headline requirement: every fixture under
// testdata/ must Parse then Emit back to the exact bytes it started as.
func TestRoundTrip(t *testing.T) {
	matches, err := filepath.Glob("testdata/*.diff")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatal("no fixtures found under testdata/")
	}
	for _, path := range matches {
		name := filepath.Base(path)
		t.Run(name, func(t *testing.T) {
			orig, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			files, err := Parse(orig)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			got := Emit(files)
			if !bytes.Equal(got, orig) {
				t.Errorf("round trip mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, orig)
			}
		})
	}
}

func TestParseModify(t *testing.T) {
	f := parseOne(t, "modify.diff")
	if f.Status != model.FileModified {
		t.Errorf("Status = %q, want modified", f.Status)
	}
	if f.OldPath != "mod.go" || f.NewPath != "mod.go" {
		t.Errorf("paths = %q/%q, want mod.go/mod.go", f.OldPath, f.NewPath)
	}
	if f.OldMode != "100644" || f.NewMode != "100644" {
		t.Errorf("modes = %q/%q, want 100644/100644", f.OldMode, f.NewMode)
	}
	if f.Binary {
		t.Error("Binary = true, want false")
	}
	if f.Additions != 2 || f.Deletions != 1 {
		t.Errorf("Additions/Deletions = %d/%d, want 2/1", f.Additions, f.Deletions)
	}
	if f.Language != "go" {
		t.Errorf("Language = %q, want go", f.Language)
	}
	if len(f.Hunks) != 1 {
		t.Fatalf("len(Hunks) = %d, want 1", len(f.Hunks))
	}
	h := f.Hunks[0]
	if h.OldStart != 1 || h.OldLines != 5 || h.NewStart != 1 || h.NewLines != 6 {
		t.Errorf("hunk range = -%d,%d +%d,%d, want -1,5 +1,6", h.OldStart, h.OldLines, h.NewStart, h.NewLines)
	}
	if len(h.Lines) != 7 {
		t.Fatalf("len(Lines) = %d, want 7", len(h.Lines))
	}
	wantPos := []int{1, 2, 3, 4, 5, 6, 7}
	for i, l := range h.Lines {
		if l.Position != wantPos[i] {
			t.Errorf("Lines[%d].Position = %d, want %d", i, l.Position, wantPos[i])
		}
	}
	if h.ID == "" {
		t.Error("hunk ID is empty")
	}
	if got := model.HunkID(f.Path(), h.Lines); got != h.ID {
		t.Errorf("hunk ID = %q, want %q (model.HunkID)", h.ID, got)
	}

	if hi, li, ok := LineAt(f, model.SideOld, 2); !ok || hi != 0 || li != 1 {
		t.Errorf("LineAt(old,2) = %d,%d,%v, want 0,1,true", hi, li, ok)
	}
	if pos, ok := PositionFor(f, model.SideNew, 6); !ok || pos != 7 {
		t.Errorf("PositionFor(new,6) = %d,%v, want 7,true", pos, ok)
	}
	if pos, ok := PositionFor(f, model.SideOld, 1); !ok || pos != 1 {
		t.Errorf("PositionFor(old,1) = %d,%v, want 1,true", pos, ok)
	}
	if _, _, ok := LineAt(f, model.SideOld, 99); ok {
		t.Error("LineAt(old,99) = ok, want not found")
	}
}

func TestParseAdded(t *testing.T) {
	f := parseOne(t, "added.diff")
	if f.Status != model.FileAdded {
		t.Errorf("Status = %q, want added", f.Status)
	}
	if f.OldPath != "" || f.NewPath != "added.go" {
		t.Errorf("paths = %q/%q, want ''/added.go", f.OldPath, f.NewPath)
	}
	if f.NewMode != "100644" || f.OldMode != "" {
		t.Errorf("modes = %q/%q, want ''/100644 old/new", f.OldMode, f.NewMode)
	}
	if len(f.Hunks) != 1 || f.Hunks[0].OldStart != 0 || f.Hunks[0].OldLines != 0 {
		t.Fatalf("hunk = %+v, want old range 0,0", f.Hunks)
	}
	if f.Additions != 2 || f.Deletions != 0 {
		t.Errorf("Additions/Deletions = %d/%d, want 2/0", f.Additions, f.Deletions)
	}
}

func TestParseDeleted(t *testing.T) {
	f := parseOne(t, "deleted.diff")
	if f.Status != model.FileDeleted {
		t.Errorf("Status = %q, want deleted", f.Status)
	}
	if f.OldPath != "del.go" || f.NewPath != "" {
		t.Errorf("paths = %q/%q, want del.go/''", f.OldPath, f.NewPath)
	}
	if f.OldMode != "100644" || f.NewMode != "" {
		t.Errorf("modes = %q/%q, want 100644/''", f.OldMode, f.NewMode)
	}
	if f.Additions != 0 || f.Deletions != 2 {
		t.Errorf("Additions/Deletions = %d/%d, want 0/2", f.Additions, f.Deletions)
	}
	// f.Path() falls back to OldPath for a deletion.
	if got := f.Path(); got != "del.go" {
		t.Errorf("Path() = %q, want del.go", got)
	}
}

func TestParseRenamePure(t *testing.T) {
	f := parseOne(t, "rename_pure.diff")
	if f.Status != model.FileRenamed {
		t.Errorf("Status = %q, want renamed", f.Status)
	}
	if f.OldPath != "renameme.go" || f.NewPath != "renamed.go" {
		t.Errorf("paths = %q/%q, want renameme.go/renamed.go", f.OldPath, f.NewPath)
	}
	if f.Similarity != 100 {
		t.Errorf("Similarity = %d, want 100", f.Similarity)
	}
	if len(f.Hunks) != 0 {
		t.Errorf("len(Hunks) = %d, want 0 (pure rename has no content change)", len(f.Hunks))
	}
}

func TestParseRenameEdit(t *testing.T) {
	f := parseOne(t, "rename_edit.diff")
	if f.Status != model.FileRenamed {
		t.Errorf("Status = %q, want renamed", f.Status)
	}
	if f.OldPath != "editrename.go" || f.NewPath != "editrenamed.go" {
		t.Errorf("paths = %q/%q, want editrename.go/editrenamed.go", f.OldPath, f.NewPath)
	}
	if f.Similarity != 54 {
		t.Errorf("Similarity = %d, want 54", f.Similarity)
	}
	if f.OldMode != "100644" || f.NewMode != "100644" {
		t.Errorf("modes = %q/%q, want 100644/100644 (unchanged, from the index line)", f.OldMode, f.NewMode)
	}
	if len(f.Hunks) != 1 || f.Additions != 1 || f.Deletions != 1 {
		t.Fatalf("hunks/add/del = %d/%d/%d, want 1/1/1", len(f.Hunks), f.Additions, f.Deletions)
	}
}

func TestParseRenameUnicode(t *testing.T) {
	f := parseOne(t, "rename_unicode.diff")
	if f.Status != model.FileRenamed {
		t.Errorf("Status = %q, want renamed", f.Status)
	}
	wantOld := "with space/café commit.go"
	wantNew := "with space/café renamed.go"
	if f.OldPath != wantOld || f.NewPath != wantNew {
		t.Errorf("paths = %q/%q, want %q/%q", f.OldPath, f.NewPath, wantOld, wantNew)
	}
}

func TestParseModeChange(t *testing.T) {
	f := parseOne(t, "mode_change.diff")
	if f.Status != model.FileModified {
		t.Errorf("Status = %q, want modified", f.Status)
	}
	if f.OldPath != "run.sh" || f.NewPath != "run.sh" {
		t.Errorf("paths = %q/%q, want run.sh/run.sh", f.OldPath, f.NewPath)
	}
	if f.OldMode != "100644" || f.NewMode != "100755" {
		t.Errorf("modes = %q/%q, want 100644/100755", f.OldMode, f.NewMode)
	}
	if len(f.Hunks) != 0 {
		t.Errorf("len(Hunks) = %d, want 0 (mode-only change has no content hunks)", len(f.Hunks))
	}
}

// TestParseModeChangeWithSpace guards the "diff --git" line's own path
// splitting: with no ---/+++ or rename line to fall back on, a space inside
// a path shared by both sides must not be mistaken for the a/-b/ separator.
func TestParseModeChangeWithSpace(t *testing.T) {
	f := parseOne(t, "mode_change_space.diff")
	want := "with space/runner.sh"
	if f.OldPath != want || f.NewPath != want {
		t.Errorf("paths = %q/%q, want %q/%q", f.OldPath, f.NewPath, want, want)
	}
	if f.OldMode != "100644" || f.NewMode != "100755" {
		t.Errorf("modes = %q/%q, want 100644/100755", f.OldMode, f.NewMode)
	}
}

func TestParseModeAndContent(t *testing.T) {
	f := parseOne(t, "mode_and_content.diff")
	if f.OldMode != "100644" || f.NewMode != "100755" {
		t.Errorf("modes = %q/%q, want 100644/100755", f.OldMode, f.NewMode)
	}
	if len(f.Hunks) != 1 || f.Additions != 1 || f.Deletions != 1 {
		t.Fatalf("hunks/add/del = %d/%d/%d, want 1/1/1", len(f.Hunks), f.Additions, f.Deletions)
	}
}

func TestParseEmptyFile(t *testing.T) {
	f := parseOne(t, "empty_file.diff")
	if f.Status != model.FileAdded {
		t.Errorf("Status = %q, want added", f.Status)
	}
	if f.NewPath != "empty.txt" || f.OldPath != "" {
		t.Errorf("paths = %q/%q, want ''/empty.txt", f.OldPath, f.NewPath)
	}
	if len(f.Hunks) != 0 {
		t.Errorf("len(Hunks) = %d, want 0 (empty file has nothing to diff)", len(f.Hunks))
	}
	if f.Binary {
		t.Error("Binary = true, want false")
	}
}

func TestParseNoNewline(t *testing.T) {
	f := parseOne(t, "no_newline.diff")
	if len(f.Hunks) != 1 || len(f.Hunks[0].Lines) != 2 {
		t.Fatalf("hunks = %+v", f.Hunks)
	}
	del, add := f.Hunks[0].Lines[0], f.Hunks[0].Lines[1]
	if !del.NoNewline || del.Kind != model.LineDel || del.Text != "no newline last line" {
		t.Errorf("del line = %+v", del)
	}
	if !add.NoNewline || add.Kind != model.LineAdd || add.Text != "no newline last line CHANGED" {
		t.Errorf("add line = %+v", add)
	}
	// The "\ No newline at end of file" marker after the del line consumes
	// position 2, so the add line lands on position 3, not 2.
	if del.Position != 1 {
		t.Errorf("del.Position = %d, want 1", del.Position)
	}
	if add.Position != 3 {
		t.Errorf("add.Position = %d, want 3 (the no-newline marker after the del line consumes position 2)", add.Position)
	}
}

func TestParseBinaryDiffer(t *testing.T) {
	f := parseOne(t, "binary_differ.diff")
	if !f.Binary {
		t.Error("Binary = false, want true")
	}
	if f.OldPath != "image.png" || f.NewPath != "image.png" {
		t.Errorf("paths = %q/%q, want image.png/image.png", f.OldPath, f.NewPath)
	}
	if f.OldMode != "100644" || f.NewMode != "100644" {
		t.Errorf("modes = %q/%q, want 100644/100644", f.OldMode, f.NewMode)
	}
	if len(f.Hunks) != 0 {
		t.Errorf("len(Hunks) = %d, want 0", len(f.Hunks))
	}
}

func TestParseBinaryGitPatch(t *testing.T) {
	f := parseOne(t, "binary_gitpatch.diff")
	if !f.Binary {
		t.Error("Binary = false, want true")
	}
	if f.OldPath != "image.png" || f.NewPath != "image.png" {
		t.Errorf("paths = %q/%q, want image.png/image.png", f.OldPath, f.NewPath)
	}
	if len(f.Header) != 9 {
		t.Errorf("len(Header) = %d, want 9 (the whole GIT binary patch block, blank lines included)", len(f.Header))
	}
	if f.Header[2] != "GIT binary patch" {
		t.Errorf("Header[2] = %q, want %q", f.Header[2], "GIT binary patch")
	}
}

func TestParseBinaryAddedDeleted(t *testing.T) {
	added := parseOne(t, "binary_new.diff")
	if added.Status != model.FileAdded || !added.Binary {
		t.Errorf("added: Status=%q Binary=%v, want added/true", added.Status, added.Binary)
	}
	if added.OldPath != "" || added.NewPath != "newimage.png" {
		t.Errorf("added paths = %q/%q, want ''/newimage.png", added.OldPath, added.NewPath)
	}

	deleted := parseOne(t, "binary_deleted.diff")
	if deleted.Status != model.FileDeleted || !deleted.Binary {
		t.Errorf("deleted: Status=%q Binary=%v, want deleted/true", deleted.Status, deleted.Binary)
	}
	if deleted.OldPath != "image.png" || deleted.NewPath != "" {
		t.Errorf("deleted paths = %q/%q, want image.png/''", deleted.OldPath, deleted.NewPath)
	}
}

func TestParseCRLF(t *testing.T) {
	f := parseOne(t, "crlf.diff")
	if len(f.Hunks) != 1 || len(f.Hunks[0].Lines) != 4 {
		t.Fatalf("hunks = %+v", f.Hunks)
	}
	lines := f.Hunks[0].Lines
	want := []string{"crlf line1\r", "crlf line2\r", "crlf CHANGED\r", "crlf line3\r"}
	for i, l := range lines {
		if l.Text != want[i] {
			t.Errorf("Lines[%d].Text = %q, want %q", i, l.Text, want[i])
		}
	}
}

func TestParseQuotedSpace(t *testing.T) {
	f := parseOne(t, "quoted_space.diff")
	if f.Status != model.FileAdded {
		t.Errorf("Status = %q, want added", f.Status)
	}
	if f.NewPath != "with space/new file.go" {
		t.Errorf("NewPath = %q, want %q", f.NewPath, "with space/new file.go")
	}
}

func TestParseQuotedUnicode(t *testing.T) {
	f := parseOne(t, "quoted_unicode.diff")
	if f.Status != model.FileAdded {
		t.Errorf("Status = %q, want added", f.Status)
	}
	want := "with space/café new.go"
	if f.NewPath != want {
		t.Errorf("NewPath = %q, want %q", f.NewPath, want)
	}
}

func TestParseCopy(t *testing.T) {
	f := parseOne(t, "copy_file.diff")
	if f.Status != model.FileCopied {
		t.Errorf("Status = %q, want copied", f.Status)
	}
	if f.OldPath != "copysrc.go" || f.NewPath != "copydst.go" {
		t.Errorf("paths = %q/%q, want copysrc.go/copydst.go", f.OldPath, f.NewPath)
	}
	if f.Similarity != 100 {
		t.Errorf("Similarity = %d, want 100", f.Similarity)
	}
}

func TestParseMulti(t *testing.T) {
	files, err := Parse(readFixture(t, "multi.diff"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 4 {
		t.Fatalf("len(files) = %d, want 4", len(files))
	}
	type want struct {
		status           model.FileStatus
		oldPath, newPath string
	}
	wants := []want{
		{model.FileAdded, "", "added.go"},
		{model.FileDeleted, "del.go", ""},
		{model.FileRenamed, "editrename.go", "editrenamed.go"},
		{model.FileModified, "mod.go", "mod.go"},
	}
	for i, w := range wants {
		f := files[i]
		if f.Status != w.status || f.OldPath != w.oldPath || f.NewPath != w.newPath {
			t.Errorf("files[%d] = %q %q/%q, want %q %q/%q", i, f.Status, f.OldPath, f.NewPath, w.status, w.oldPath, w.newPath)
		}
		// Position numbering restarts at each file.
		if len(f.Hunks) > 0 && f.Hunks[0].Lines[0].Position != 1 {
			t.Errorf("files[%d]: first hunk's first line Position = %d, want 1", i, f.Hunks[0].Lines[0].Position)
		}
	}
}

func TestParseMultiHunkPositions(t *testing.T) {
	f := parseOne(t, "multi_hunk.diff")
	if len(f.Hunks) != 2 {
		t.Fatalf("len(Hunks) = %d, want 2", len(f.Hunks))
	}
	if f.Hunks[1].Section != "func Fourth() int {" {
		t.Errorf("Hunks[1].Section = %q, want %q", f.Hunks[1].Section, "func Fourth() int {")
	}
	var gotPos []int
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			gotPos = append(gotPos, l.Position)
		}
	}
	// Hunk 1 has 8 lines: positions 1-8 (it is the file's first hunk, so its
	// own "@@" header consumes no position). Hunk 2's "@@" header is not the
	// first for this file, so it consumes position 9 on its own; hunk 2's 6
	// lines then land on 10-15.
	want := []int{1, 2, 3, 4, 5, 6, 7, 8, 10, 11, 12, 13, 14, 15}
	if len(gotPos) != len(want) {
		t.Fatalf("got %d positions %v, want %d %v", len(gotPos), gotPos, len(want), want)
	}
	for i := range want {
		if gotPos[i] != want[i] {
			t.Errorf("position[%d] = %d, want %d (full: %v)", i, gotPos[i], want[i], gotPos)
		}
	}
}

func TestParsePlainUnifiedDiff(t *testing.T) {
	f := parseOne(t, "plain.diff")
	if f.Status != model.FileModified {
		t.Errorf("Status = %q, want modified", f.Status)
	}
	if f.OldPath != "src/util.go" || f.NewPath != "src/util.go" {
		t.Errorf("paths = %q/%q, want src/util.go/src/util.go", f.OldPath, f.NewPath)
	}
	if len(f.Header) != 2 {
		t.Errorf("len(Header) = %d, want 2 (no diff --git line in a plain patch)", len(f.Header))
	}
	if len(f.Hunks) != 1 || f.Additions != 2 || f.Deletions != 1 {
		t.Fatalf("hunks/add/del = %d/%d/%d, want 1/2/1", len(f.Hunks), f.Additions, f.Deletions)
	}
}

func TestUnquoteGitPath(t *testing.T) {
	cases := map[string]string{
		"plain.go":                  "plain.go",
		`"with space.go"`:           "with space.go",
		`"with\ttab.go"`:            "with\ttab.go",
		`"with\\backslash.go"`:      `with\backslash.go`,
		`"with\"quote.go"`:          `with"quote.go`,
		`"caf\303\251.go"`:          "café.go",
		`"mix \303\251 and\tplain"`: "mix é and\tplain",
	}
	for in, want := range cases {
		if got := unquoteGitPath(in); got != want {
			t.Errorf("unquoteGitPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseDiffGitLine(t *testing.T) {
	cases := []struct {
		line             string
		wantOld, wantNew string
	}{
		{"diff --git a/foo.go b/foo.go", "a/foo.go", "b/foo.go"},
		{"diff --git a/foo.go b/bar.go", "a/foo.go", "b/bar.go"},
		{`diff --git "a/caf\303\251.go" "b/caf\303\251.go"`, "a/café.go", "b/café.go"},
		{"diff --git a/with space/f.go b/with space/f.go", "a/with space/f.go", "b/with space/f.go"},
	}
	for _, c := range cases {
		old, nw, ok := parseDiffGitLine(c.line)
		if !ok {
			t.Errorf("parseDiffGitLine(%q): ok=false", c.line)
			continue
		}
		// Normalize quoted results for comparison against the unquoted
		// wants in the unicode case.
		oldNorm, nwNorm := unquoteGitPath(old), unquoteGitPath(nw)
		wantOld, wantNew := unquoteGitPath(c.wantOld), unquoteGitPath(c.wantNew)
		if oldNorm != wantOld || nwNorm != wantNew {
			t.Errorf("parseDiffGitLine(%q) = %q,%q want %q,%q", c.line, old, nw, c.wantOld, c.wantNew)
		}
	}
}

func TestParseErrors(t *testing.T) {
	cases := [][]byte{
		[]byte("not a diff at all\n"),
		[]byte("--- a/f\n@@ -1 +1 @@\n no +++ line\n"),
	}
	for _, in := range cases {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q): want error, got nil", in)
		}
	}
}
