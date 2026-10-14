package session

import "testing"

func TestRepoKeySameRemoteDifferentSpellings(t *testing.T) {
	spellings := []string{
		"git@github.com:o/r.git",
		"git@github.com:o/r",
		"https://github.com/o/r",
		"https://github.com/o/r.git",
		"https://github.com/o/r/",
		"ssh://git@github.com/o/r.git",
	}
	want := RepoKey(spellings[0])
	for _, s := range spellings[1:] {
		if got := RepoKey(s); got != want {
			t.Errorf("RepoKey(%q) = %q, want %q (same remote as %q)", s, got, want, spellings[0])
		}
	}
}

func TestRepoKeyDiffers(t *testing.T) {
	a := RepoKey("https://github.com/o/r")
	b := RepoKey("https://github.com/o/other")
	if a == b {
		t.Error("different repos must not collide")
	}
	c := RepoKey("https://gitlab.com/o/r")
	if a == c {
		t.Error("different hosts must not collide")
	}
}

func TestRepoKeyLocalPath(t *testing.T) {
	a := RepoKey("/tmp/repo")
	b := RepoKey("/tmp/repo/")
	if a != b {
		t.Error("trailing slash must not change the key")
	}
	c := RepoKey("/tmp/other")
	if a == c {
		t.Error("different paths must not collide")
	}
}

func TestRepoKeyShape(t *testing.T) {
	k := RepoKey("https://github.com/o/r")
	if len(k) != 16 {
		t.Fatalf("want 16 hex chars, got %d: %q", len(k), k)
	}
	for _, r := range k {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			t.Fatalf("not lowercase hex: %q", k)
		}
	}
}

func TestRepoKeyDeterministic(t *testing.T) {
	if RepoKey("https://github.com/o/r") != RepoKey("https://github.com/o/r") {
		t.Error("RepoKey must be deterministic")
	}
}
