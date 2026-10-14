package github

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/iszlai/tools/flowreview/internal/host"
	"github.com/iszlai/tools/flowreview/internal/model"
)

func TestListComments_pagination(t *testing.T) {
	ref := model.PRRef{Owner: "cli", Repo: "cli", Number: 9999}

	mux := http.NewServeMux()
	page1Hits, page2Hits := 0, 0
	mux.HandleFunc("/repos/cli/cli/pulls/9999/comments", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		if r.URL.Query().Get("page") == "2" {
			page2Hits++
			w.Write(readTestdata(t, "review_comments_page2.json"))
			return
		}
		page1Hits++
		// Point at page 2 the same way GitHub does: an absolute URL in Link.
		next := fmt.Sprintf("http://%s/repos/cli/cli/pulls/9999/comments?page=2", r.Host)
		w.Header().Set("Link", fmt.Sprintf(`<%s>; rel="next", <%s>; rel="last"`, next, next))
		w.Write(readTestdata(t, "review_comments_page1.json"))
	})
	mux.HandleFunc("/repos/cli/cli/issues/9999/comments", func(w http.ResponseWriter, r *http.Request) {
		w.Write(readTestdata(t, "issue_comments.json"))
	})
	c := newTestClient(t, mux)

	comments, err := c.ListComments(context.Background(), ref)
	if err != nil {
		t.Fatalf("ListComments: %v", err)
	}
	if page1Hits != 1 || page2Hits != 1 {
		t.Fatalf("review comment pages hit: page1=%d page2=%d, want 1 each", page1Hits, page2Hits)
	}
	if len(comments) != 3 {
		t.Fatalf("len(comments) = %d, want 3 (2 review comments across 2 pages + 1 issue comment)", len(comments))
	}

	byID := map[int64]host.RemoteComment{}
	for _, c := range comments {
		byID[c.ID] = c
	}

	first, ok := byID[101]
	if !ok {
		t.Fatal("missing comment 101 (page 1)")
	}
	if first.Outdated {
		t.Error("comment 101: Outdated = true, want false (line is non-null)")
	}
	if first.Line != 13 || first.Side != model.SideNew {
		t.Errorf("comment 101: Line=%d Side=%q, want 13/new", first.Line, first.Side)
	}
	if first.Path != "internal/repo/list.go" {
		t.Errorf("comment 101: Path = %q", first.Path)
	}

	second, ok := byID[102]
	if !ok {
		t.Fatal("missing comment 102 (page 2)")
	}
	if !second.Outdated {
		t.Error("comment 102: Outdated = false, want true (line is null)")
	}
	if second.Line != 9 {
		t.Errorf("comment 102: Line = %d, want original_line 9 when outdated", second.Line)
	}
	if second.Side != model.SideOld {
		t.Errorf("comment 102: Side = %q, want old (LEFT)", second.Side)
	}
	if second.InReplyTo != 101 {
		t.Errorf("comment 102: InReplyTo = %d, want 101", second.InReplyTo)
	}

	general, ok := byID[201]
	if !ok {
		t.Fatal("missing general comment 201")
	}
	if general.Path != "" || general.Line != 0 {
		t.Errorf("general comment 201 should have no anchor, got Path=%q Line=%d", general.Path, general.Line)
	}
	if general.Author != "maintainer-a" {
		t.Errorf("general comment 201 Author = %q", general.Author)
	}
}

func TestListReviews(t *testing.T) {
	ref := model.PRRef{Owner: "cli", Repo: "cli", Number: 9999}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/cli/cli/pulls/9999/reviews", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		w.Write(readTestdata(t, "reviews.json"))
	})
	c := newTestClient(t, mux)

	reviews, err := c.ListReviews(context.Background(), ref)
	if err != nil {
		t.Fatalf("ListReviews: %v", err)
	}
	if len(reviews) != 2 {
		t.Fatalf("len(reviews) = %d, want 2", len(reviews))
	}
	if reviews[0].State != "COMMENTED" || reviews[0].Author != "reviewer-one" {
		t.Errorf("reviews[0] = %+v", reviews[0])
	}
	if reviews[1].State != "APPROVED" || reviews[1].Author != "maintainer-a" {
		t.Errorf("reviews[1] = %+v", reviews[1])
	}
}
