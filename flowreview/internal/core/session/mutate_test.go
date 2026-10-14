package session

import (
	"testing"

	"github.com/iszlai/tools/flowreview/internal/model"
)

func hunkLines() []model.DiffLine {
	return []model.DiffLine{
		{Kind: model.LineContext, Text: "func f() {", OldNo: 1, NewNo: 1},
		{Kind: model.LineDel, Text: "\treturn 1", OldNo: 2},
		{Kind: model.LineAdd, Text: "\treturn 2", NewNo: 2},
		{Kind: model.LineContext, Text: "}", OldNo: 3, NewNo: 3},
	}
}

func TestMarkReviewedAndUnmark(t *testing.T) {
	s := New(t.TempDir())
	sess := newSession("repo1", "sess1")
	if err := s.Create(sess); err != nil {
		t.Fatal(err)
	}
	hunk := model.DiffHunk{ID: "h1", Lines: hunkLines()}

	if err := s.MarkReviewed("repo1", "sess1", hunk, "a.go", "headsha"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("repo1", "sess1")
	if err != nil {
		t.Fatal(err)
	}
	st := got.States["h1"]
	if st == nil {
		t.Fatal("want a state for h1")
	}
	if st.Status != model.HunkReviewed {
		t.Errorf("want reviewed, got %s", st.Status)
	}
	if st.Body != model.HunkBody(hunk.Lines) {
		t.Error("want stored body to match the reviewed hunk's body")
	}
	if st.Path != "a.go" || st.ReviewedHead != "headsha" {
		t.Errorf("got %+v", st)
	}
	if st.ReviewedAt.IsZero() {
		t.Error("want ReviewedAt set")
	}

	if err := s.Unmark("repo1", "sess1", "h1"); err != nil {
		t.Fatal(err)
	}
	got, err = s.Get("repo1", "sess1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got.States["h1"]; ok {
		t.Error("want the state removed after Unmark")
	}
}

func TestUpsertCommentInsertAndUpdate(t *testing.T) {
	s := New(t.TempDir())
	sess := newSession("repo1", "sess1")
	if err := s.Create(sess); err != nil {
		t.Fatal(err)
	}

	c, err := s.UpsertComment("repo1", "sess1", model.CommentDraft{Body: "first draft"})
	if err != nil {
		t.Fatal(err)
	}
	if c.ID == "" {
		t.Fatal("want an assigned id")
	}
	if c.CreatedAt.IsZero() || c.UpdatedAt.IsZero() {
		t.Error("want timestamps set on insert")
	}
	createdAt := c.CreatedAt

	c.Body = "edited"
	c2, err := s.UpsertComment("repo1", "sess1", c)
	if err != nil {
		t.Fatal(err)
	}
	if c2.ID != c.ID {
		t.Error("update must keep the same id")
	}
	if !c2.CreatedAt.Equal(createdAt) {
		t.Error("update must preserve the original CreatedAt")
	}

	got, err := s.Get("repo1", "sess1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Comments) != 1 {
		t.Fatalf("want exactly one comment (update, not append), got %d", len(got.Comments))
	}
	if got.Comments[0].Body != "edited" {
		t.Errorf("got body %q", got.Comments[0].Body)
	}
}

func TestUpsertCommentUnknownIDErrors(t *testing.T) {
	s := New(t.TempDir())
	sess := newSession("repo1", "sess1")
	if err := s.Create(sess); err != nil {
		t.Fatal(err)
	}
	_, err := s.UpsertComment("repo1", "sess1", model.CommentDraft{ID: "nope", Body: "x"})
	if err == nil {
		t.Fatal("want an error updating a comment id that does not exist")
	}
}

func TestDeleteComment(t *testing.T) {
	s := New(t.TempDir())
	sess := newSession("repo1", "sess1")
	if err := s.Create(sess); err != nil {
		t.Fatal(err)
	}
	c, err := s.UpsertComment("repo1", "sess1", model.CommentDraft{Body: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteComment("repo1", "sess1", c.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get("repo1", "sess1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Comments) != 0 {
		t.Errorf("want no comments left, got %d", len(got.Comments))
	}
	// Deleting an id that is not there is not an error.
	if err := s.DeleteComment("repo1", "sess1", "gone"); err != nil {
		t.Fatalf("deleting a missing comment must not error: %v", err)
	}
}
