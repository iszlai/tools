package session

import (
	"errors"
	"testing"

	"github.com/iszlai/tools/flowreview/internal/model"
)

func TestNewID(t *testing.T) {
	cases := []struct {
		name string
		src  model.Source
		want string
	}{
		{
			"pr",
			model.Source{Kind: model.SourcePR, PR: &model.PRRef{Owner: "Upvest", Repo: "ledger", Number: 123}},
			"pr-upvest-ledger-123",
		},
		{"worktree", model.Source{Kind: model.SourceWorktree, RepoPath: "/repo"}, "worktree"},
	}
	for _, c := range cases {
		if got := NewID(c.src); got != c.want {
			t.Errorf("%s: NewID() = %q, want %q", c.name, got, c.want)
		}
	}

	// Range and commit sources are derived from Ref and must be deterministic
	// and prefixed by kind.
	range1 := NewID(model.Source{Kind: model.SourceRange, Ref: "main...feature"})
	range2 := NewID(model.Source{Kind: model.SourceRange, Ref: "main...feature"})
	if range1 != range2 {
		t.Errorf("range id must be deterministic: %q != %q", range1, range2)
	}
	if range1[:6] != "range-" {
		t.Errorf("want range- prefix, got %q", range1)
	}

	// A Source with no natural key (e.g. a patch without a path) falls back to
	// a random id rather than colliding with every other such source.
	a := NewID(model.Source{Kind: model.SourcePatch})
	b := NewID(model.Source{Kind: model.SourcePatch})
	if a == b {
		t.Error("fallback ids must not collide")
	}
}

func TestFindBySourceResumesSamePR(t *testing.T) {
	s := New(t.TempDir())
	src := model.Source{
		Kind: model.SourcePR,
		PR:   &model.PRRef{Host: "github.com", Owner: "o", Repo: "r", Number: 7},
	}
	sess := newSession("repo1", NewID(src))
	sess.Source = src
	if err := s.Create(sess); err != nil {
		t.Fatal(err)
	}

	// The PR advances (new head sha) but is still the same PR: FindBySource
	// must resume it rather than treat it as new.
	again := src
	again.HeadSHA = "deadbeef"
	got, err := s.FindBySource("repo1", again)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != sess.ID {
		t.Errorf("got session %q, want %q", got.ID, sess.ID)
	}
}

func TestFindBySourceDistinguishesDifferentPRs(t *testing.T) {
	s := New(t.TempDir())
	src1 := model.Source{Kind: model.SourcePR, PR: &model.PRRef{Owner: "o", Repo: "r", Number: 1}}
	sess := newSession("repo1", NewID(src1))
	sess.Source = src1
	if err := s.Create(sess); err != nil {
		t.Fatal(err)
	}

	src2 := model.Source{Kind: model.SourcePR, PR: &model.PRRef{Owner: "o", Repo: "r", Number: 2}}
	if _, err := s.FindBySource("repo1", src2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound for a different PR number, got %v", err)
	}
}

func TestFindBySourceNoMatch(t *testing.T) {
	s := New(t.TempDir())
	_, err := s.FindBySource("repo1", model.Source{Kind: model.SourceWorktree, RepoPath: "/repo"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
