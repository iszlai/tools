package git

import (
	"context"
	"testing"
)

func TestLog_rangeAndOrder(t *testing.T) {
	f := newFixture(t)
	commits, err := f.repo.Log(context.Background(), f.sha["root"]+".."+f.sha["binary"])
	if err != nil {
		t.Fatalf("Log: %v", err)
	}

	wantSHAs := []string{f.sha["binary"], f.sha["delete"], f.sha["rename"], f.sha["modify"]}
	if len(commits) != len(wantSHAs) {
		t.Fatalf("Log returned %d commits, want %d: %+v", len(commits), len(wantSHAs), commits)
	}
	for i, c := range commits {
		if c.SHA != wantSHAs[i] {
			t.Errorf("commits[%d].SHA = %s, want %s", i, c.SHA, wantSHAs[i])
		}
		if c.Author != "Fixture" || c.AuthorEmail != "fixture@example.com" {
			t.Errorf("commits[%d] author = %s <%s>, want Fixture <fixture@example.com>", i, c.Author, c.AuthorEmail)
		}
		if c.Time.IsZero() {
			t.Errorf("commits[%d].Time is zero", i)
		}
	}
	// Newest first.
	for i := 1; i < len(commits); i++ {
		if !commits[i-1].Time.After(commits[i].Time) {
			t.Errorf("commits not newest-first: commits[%d].Time=%v, commits[%d].Time=%v",
				i-1, commits[i-1].Time, i, commits[i].Time)
		}
	}
}

func TestLog_fileChanges(t *testing.T) {
	f := newFixture(t)
	commits, err := f.repo.Log(context.Background(), f.sha["root"]+".."+f.sha["binary"])
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	bySHA := map[string]Commit{}
	for _, c := range commits {
		bySHA[c.SHA] = c
	}

	binary := bySHA[f.sha["binary"]]
	if len(binary.Files) != 1 || binary.Files[0].Status != "A" || binary.Files[0].Path != "bin.dat" {
		t.Errorf("binary commit files = %+v, want one add of bin.dat", binary.Files)
	}

	del := bySHA[f.sha["delete"]]
	if len(del.Files) != 1 || del.Files[0].Status != "D" || del.Files[0].Path != "a.txt" {
		t.Errorf("delete commit files = %+v, want one delete of a.txt", del.Files)
	}

	rename := bySHA[f.sha["rename"]]
	if len(rename.Files) != 1 {
		t.Fatalf("rename commit files = %+v, want exactly one entry", rename.Files)
	}
	rf := rename.Files[0]
	if rf.Status != "R100" || rf.OldPath != "b.txt" || rf.Path != "renamed.txt" {
		t.Errorf("rename entry = %+v, want {R100, b.txt, renamed.txt}", rf)
	}

	modify := bySHA[f.sha["modify"]]
	var sawEditA, sawAddB bool
	for _, fc := range modify.Files {
		switch {
		case fc.Status == "M" && fc.Path == "a.txt":
			sawEditA = true
		case fc.Status == "A" && fc.Path == "b.txt":
			sawAddB = true
		}
	}
	if !sawEditA || !sawAddB {
		t.Errorf("modify commit files = %+v, want an edit of a.txt and an add of b.txt", modify.Files)
	}
}

func TestLog_subject(t *testing.T) {
	f := newFixture(t)
	commits, err := f.repo.Log(context.Background(), f.sha["modify"])
	if err != nil {
		t.Fatalf("Log: %v", err)
	}
	if len(commits) == 0 {
		t.Fatal("Log returned no commits")
	}
	if commits[0].Subject != "modify: edit a.txt, add b.txt" {
		t.Errorf("Subject = %q", commits[0].Subject)
	}
}
