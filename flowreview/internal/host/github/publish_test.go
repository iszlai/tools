package github

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/iszlai/tools/flowreview/internal/host"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// TestClient_satisfiesHost pins Client to the host.Host interface at compile
// time: if either changes shape incompatibly, this file fails to build.
var _ host.Host = (*Client)(nil)

func draftAt(id, path string, side model.Side, line int) model.CommentDraft {
	return model.CommentDraft{
		ID:     id,
		Anchor: &model.Anchor{Path: path, Side: side, Line: line},
		Body:   "comment " + id,
	}
}

func TestSubmitReview_success(t *testing.T) {
	ref := model.PRRef{Owner: "o", Repo: "r", Number: 1}
	var gotBody map[string]any

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/pulls/1/reviews", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id": 555, "html_url": "https://github.com/o/r/pull/1#pullrequestreview-555"}`))
	})
	c := newTestClient(t, mux)

	review := host.Review{
		Event:    host.EventComment,
		Body:     "looks good overall",
		CommitID: "headsha123",
		Comments: []model.CommentDraft{
			draftAt("c1", "a.txt", model.SideNew, 2),
			draftAt("c2", "b.txt", model.SideOld, 5),
		},
	}
	res, err := c.SubmitReview(context.Background(), ref, review)
	if err != nil {
		t.Fatalf("SubmitReview: %v", err)
	}
	if res.ID != 555 || res.HTMLURL != "https://github.com/o/r/pull/1#pullrequestreview-555" {
		t.Errorf("result = %+v", res)
	}
	if len(res.Comments) != 2 || !res.Comments[0].OK || !res.Comments[1].OK {
		t.Errorf("comments = %+v, want both OK", res.Comments)
	}
	if res.Comments[0].DraftID != "c1" || res.Comments[1].DraftID != "c2" {
		t.Errorf("comments = %+v, want draft ids preserved in order", res.Comments)
	}

	// Request shape and side mapping.
	if gotBody["commit_id"] != "headsha123" || gotBody["event"] != "COMMENT" || gotBody["body"] != "looks good overall" {
		t.Errorf("request top-level fields = %+v", gotBody)
	}
	comments, ok := gotBody["comments"].([]any)
	if !ok || len(comments) != 2 {
		t.Fatalf("request comments = %+v, want 2", gotBody["comments"])
	}
	first := comments[0].(map[string]any)
	if first["path"] != "a.txt" || first["line"] != float64(2) || first["side"] != "RIGHT" || first["body"] != "comment c1" {
		t.Errorf("comments[0] = %+v, want new side mapped to RIGHT", first)
	}
	second := comments[1].(map[string]any)
	if second["side"] != "LEFT" {
		t.Errorf("comments[1].side = %v, want old side mapped to LEFT", second["side"])
	}
}

func TestSubmitReview_generalCommentsAreSkipped(t *testing.T) {
	// A nil-Anchor draft reaching SubmitReview (it shouldn't -- the review
	// package folds general comments into Body before calling this) must not
	// be sent as a malformed inline comment nor appear in the results.
	ref := model.PRRef{Owner: "o", Repo: "r", Number: 1}
	var gotBody map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/pulls/1/reviews", func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Write([]byte(`{"id": 1, "html_url": "u"}`))
	})
	c := newTestClient(t, mux)

	res, err := c.SubmitReview(context.Background(), ref, host.Review{
		Event: host.EventComment,
		Comments: []model.CommentDraft{
			{ID: "general", Body: "general note"}, // no Anchor
			draftAt("c1", "a.txt", model.SideNew, 2),
		},
	})
	if err != nil {
		t.Fatalf("SubmitReview: %v", err)
	}
	if len(res.Comments) != 1 || res.Comments[0].DraftID != "c1" {
		t.Errorf("comments = %+v, want only the anchored draft", res.Comments)
	}
	if gotBody["comments"] != nil && len(gotBody["comments"].([]any)) != 1 {
		t.Errorf("request comments = %+v, want only the anchored one sent", gotBody["comments"])
	}
}

// TestSubmitReview_partialFailureFallsBackToIndividual is the 422 case: the
// batch review is rejected, so SubmitReview submits the body alone and then
// publishes each comment one at a time -- one succeeds, one is rejected
// (a "bad line" 422 on that single comment), giving a mixed per-comment
// result instead of losing every comment in the batch.
func TestSubmitReview_partialFailureFallsBackToIndividual(t *testing.T) {
	ref := model.PRRef{Owner: "o", Repo: "r", Number: 1}
	reviewPostCount := 0

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/pulls/1/reviews", func(w http.ResponseWriter, r *http.Request) {
		reviewPostCount++
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if comments, _ := body["comments"].([]any); len(comments) > 0 {
			// The batch attempt (carries comments): reject it wholesale.
			w.WriteHeader(http.StatusUnprocessableEntity)
			w.Write([]byte(`{"message": "Validation Failed", "errors": [{"code": "custom", "field": "line"}]}`))
			return
		}
		// The body-only fallback: always succeeds.
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id": 777, "html_url": "https://github.com/o/r/pull/1#pullrequestreview-777"}`))
	})
	mux.HandleFunc("/repos/o/r/pulls/1/comments", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["path"] == "bad.txt" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			w.Write([]byte(`{"message": "pull_request_review_thread.line is invalid"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id": 42, "html_url": "https://github.com/o/r/pull/1#discussion_r42"}`))
	})
	c := newTestClient(t, mux)

	res, err := c.SubmitReview(context.Background(), ref, host.Review{
		Event:    host.EventComment,
		Body:     "top level note",
		CommitID: "headsha123",
		Comments: []model.CommentDraft{
			draftAt("good", "a.txt", model.SideNew, 2),
			draftAt("bad", "bad.txt", model.SideNew, 999),
		},
	})
	if err != nil {
		t.Fatalf("SubmitReview: %v", err)
	}
	if reviewPostCount != 2 {
		t.Fatalf("reviews endpoint hit %d times, want 2 (batch attempt + body-only fallback)", reviewPostCount)
	}
	if res.ID != 777 {
		t.Errorf("result.ID = %d, want the body-only fallback's review id", res.ID)
	}
	if len(res.Comments) != 2 {
		t.Fatalf("comments = %+v, want 2 results", res.Comments)
	}
	byID := map[string]host.CommentResult{}
	for _, cr := range res.Comments {
		byID[cr.DraftID] = cr
	}
	if !byID["good"].OK || byID["good"].RemoteID != 42 {
		t.Errorf("good = %+v, want OK with a remote id from the individual publish", byID["good"])
	}
	if byID["bad"].OK || byID["bad"].Error == "" {
		t.Errorf("bad = %+v, want a failure with a non-empty Error", byID["bad"])
	}
}

func TestSubmitReview_bodyOnlyFallbackAlsoFails(t *testing.T) {
	ref := model.PRRef{Owner: "o", Repo: "r", Number: 1}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/pulls/1/reviews", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"message": "nope"}`))
	})
	c := newTestClient(t, mux)

	_, err := c.SubmitReview(context.Background(), ref, host.Review{
		Event:    host.EventComment,
		CommitID: "sha",
		Comments: []model.CommentDraft{draftAt("c1", "a.txt", model.SideNew, 2)},
	})
	if err == nil {
		t.Fatal("SubmitReview: want an error when even the body-only fallback 422s")
	}
}

func TestPublishComment_success(t *testing.T) {
	ref := model.PRRef{Owner: "o", Repo: "r", Number: 1}
	var gotBody map[string]any
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/pulls/1/comments", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id": 99, "html_url": "https://github.com/o/r/pull/1#discussion_r99"}`))
	})
	c := newTestClient(t, mux)

	draft := draftAt("c1", "a.txt", model.SideOld, 7)
	cr, err := c.PublishComment(context.Background(), ref, "headsha", draft)
	if err != nil {
		t.Fatalf("PublishComment: %v", err)
	}
	if !cr.OK || cr.DraftID != "c1" || cr.RemoteID != 99 || cr.HTMLURL == "" {
		t.Errorf("result = %+v", cr)
	}
	if gotBody["commit_id"] != "headsha" || gotBody["path"] != "a.txt" || gotBody["line"] != float64(7) || gotBody["side"] != "LEFT" {
		t.Errorf("request body = %+v", gotBody)
	}
}

// TestPublishComment_badLine422 covers a comment whose anchor line no longer
// exists on the diff GitHub has for commit_id.
func TestPublishComment_badLine422(t *testing.T) {
	ref := model.PRRef{Owner: "o", Repo: "r", Number: 1}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/pulls/1/comments", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"message": "pull_request_review_thread.line is invalid"}`))
	})
	c := newTestClient(t, mux)

	_, err := c.PublishComment(context.Background(), ref, "headsha", draftAt("c1", "a.txt", model.SideNew, 9999))
	if err == nil {
		t.Fatal("PublishComment: want an error for a bad line")
	}
	if !isErr(err, ErrUnprocessable) {
		t.Errorf("error = %v, want wrapping ErrUnprocessable", err)
	}
}

func TestPublishComment_requiresAnchor(t *testing.T) {
	c := &Client{token: "t", baseURL: defaultBaseURL, http: http.DefaultClient}
	_, err := c.PublishComment(context.Background(), model.PRRef{Owner: "o", Repo: "r", Number: 1}, "sha", model.CommentDraft{ID: "general", Body: "no anchor"})
	if err == nil {
		t.Fatal("PublishComment: want an error for a draft with no anchor")
	}
}
