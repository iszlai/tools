package order

import (
	"testing"

	"github.com/iszlai/tools/flowreview/internal/model"
)

func changedFile(id, path string) model.ChangedFile {
	return model.ChangedFile{ID: id, OldPath: path, NewPath: path, Status: model.FileModified}
}

func pathsOf(entries []FileEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Path
	}
	return out
}

func assertPaths(t *testing.T, got []FileEntry, want ...string) {
	t.Helper()
	gotPaths := pathsOf(got)
	if len(gotPaths) != len(want) {
		t.Fatalf("got %d entries %v, want %d %v", len(gotPaths), gotPaths, len(want), want)
	}
	for i := range want {
		if gotPaths[i] != want[i] {
			t.Errorf("entry %d: got %q, want %q (full: %v)", i, gotPaths[i], want[i], gotPaths)
		}
	}
}

func TestAlphabetical(t *testing.T) {
	in := Inputs{Files: []model.ChangedFile{
		changedFile("1", "b/two.go"),
		changedFile("2", "a/one.go"),
		changedFile("3", "c/three.go"),
	}}
	o := Alphabetical(in)
	if o.Kind != KindAlpha {
		t.Errorf("Kind = %q, want %q", o.Kind, KindAlpha)
	}
	if len(o.Groups) != 0 {
		t.Errorf("Groups = %v, want none", o.Groups)
	}
	assertPaths(t, o.Unclassified, "a/one.go", "b/two.go", "c/three.go")
}

func TestDirectory(t *testing.T) {
	in := Inputs{Files: []model.ChangedFile{
		changedFile("1", "b/two.go"),
		changedFile("2", "a/z.go"),
		changedFile("3", "a/one.go"),
	}}
	o := Directory(in)
	if o.Kind != KindDirectory {
		t.Errorf("Kind = %q, want %q", o.Kind, KindDirectory)
	}
	assertPaths(t, o.Unclassified, "a/one.go", "a/z.go", "b/two.go")
}

func TestDiffOrder(t *testing.T) {
	in := Inputs{Files: []model.ChangedFile{
		changedFile("1", "z.go"),
		changedFile("2", "a.go"),
	}}
	o := DiffOrder(in)
	if o.Kind != KindDiff {
		t.Errorf("Kind = %q, want %q", o.Kind, KindDiff)
	}
	assertPaths(t, o.Unclassified, "z.go", "a.go")
}

func TestCommits_Unsupported(t *testing.T) {
	_, err := Commits(Inputs{})
	if err != ErrUnsupported {
		t.Errorf("Commits: err = %v, want ErrUnsupported", err)
	}
}

func TestBuild_Dispatch(t *testing.T) {
	in := Inputs{Files: []model.ChangedFile{changedFile("1", "a.go")}}
	for _, kind := range []string{KindFlow, KindAlpha, KindDirectory, KindDiff} {
		o, err := Build(kind, in)
		if err != nil {
			t.Errorf("Build(%q): %v", kind, err)
		}
		if o.Kind != kind {
			t.Errorf("Build(%q).Kind = %q", kind, o.Kind)
		}
	}
	if _, err := Build(KindCommits, in); err != ErrUnsupported {
		t.Errorf("Build(commits): err = %v, want ErrUnsupported", err)
	}
	if _, err := Build("bogus", in); err == nil {
		t.Error("Build(bogus): want an error")
	}
}

func TestAlphabetical_HotspotReported(t *testing.T) {
	in := Inputs{
		Files:   []model.ChangedFile{changedFile("1", "a.go")},
		Hotspot: func(path string) float64 { return 0.75 },
	}
	o := Alphabetical(in)
	if len(o.Unclassified) != 1 || o.Unclassified[0].Hotspot != 0.75 {
		t.Errorf("Unclassified = %+v, want hotspot 0.75", o.Unclassified)
	}
}
