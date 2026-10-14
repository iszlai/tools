package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/iszlai/tools/flowreview/internal/model"
)

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read testdata/%s: %v", name, err)
	}
	return b
}

// newTestClient starts an httptest server driven by mux and returns a Client
// pointed at it, so no test ever makes a real network call.
func newTestClient(t *testing.T, mux *http.ServeMux) *Client {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	c, err := New(Options{Token: "test-token", BaseURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func requireAuth(t *testing.T, r *http.Request) {
	t.Helper()
	if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
		t.Errorf("Authorization header = %q, want %q", got, "Bearer test-token")
	}
}

func TestFetchPR(t *testing.T) {
	ref := model.PRRef{Host: "github.com", Owner: "cli", Repo: "cli", Number: 9999}

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/cli/cli/pulls/9999", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Errorf("Accept header = %q, want JSON media type", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(readTestdata(t, "pr.json"))
	})
	c := newTestClient(t, mux)

	pr, err := c.FetchPR(context.Background(), ref)
	if err != nil {
		t.Fatalf("FetchPR: %v", err)
	}

	if pr.Title != "fix: handle empty repo list in `gh repo list`" {
		t.Errorf("Title = %q", pr.Title)
	}
	if pr.Author != "octocat" {
		t.Errorf("Author = %q, want octocat", pr.Author)
	}
	if pr.State != "open" {
		t.Errorf("State = %q, want open", pr.State)
	}
	if pr.BaseRef != "trunk" || pr.BaseSHA != "0123456789abcdef0123456789abcdef01234567" {
		t.Errorf("Base = %q/%q", pr.BaseRef, pr.BaseSHA)
	}
	if pr.HeadRef != "fix-empty-repo-list" || pr.HeadSHA != "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678" {
		t.Errorf("Head = %q/%q", pr.HeadRef, pr.HeadSHA)
	}
	if pr.CloneURL != "https://github.com/octocat/cli.git" {
		t.Errorf("CloneURL = %q", pr.CloneURL)
	}
	if pr.HTMLURL != "https://github.com/cli/cli/pull/9999" {
		t.Errorf("HTMLURL = %q", pr.HTMLURL)
	}
	if pr.Draft {
		t.Error("Draft = true, want false")
	}
	if pr.Ref != ref {
		t.Errorf("Ref = %+v, want %+v", pr.Ref, ref)
	}
}

func TestFetchPR_mergedAndForkDeleted(t *testing.T) {
	ref := model.PRRef{Owner: "cli", Repo: "cli", Number: 8888}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/cli/cli/pulls/8888", func(w http.ResponseWriter, r *http.Request) {
		w.Write(readTestdata(t, "pr_merged.json"))
	})
	c := newTestClient(t, mux)

	pr, err := c.FetchPR(context.Background(), ref)
	if err != nil {
		t.Fatalf("FetchPR: %v", err)
	}
	// merged:true overrides the raw state:"closed" GitHub also sends.
	if pr.State != "merged" {
		t.Errorf("State = %q, want merged", pr.State)
	}
	// head.repo was null (source fork deleted) — must not panic, CloneURL empty.
	if pr.CloneURL != "" {
		t.Errorf("CloneURL = %q, want empty when head.repo is null", pr.CloneURL)
	}
}

func TestFetchDiff(t *testing.T) {
	ref := model.PRRef{Owner: "cli", Repo: "cli", Number: 9999}
	want := readTestdata(t, "diff.txt")

	mux := http.NewServeMux()
	mux.HandleFunc("/repos/cli/cli/pulls/9999", func(w http.ResponseWriter, r *http.Request) {
		requireAuth(t, r)
		if got := r.Header.Get("Accept"); got != "application/vnd.github.v3.diff" {
			t.Fatalf("Accept header = %q, want the v3 diff media type", got)
		}
		w.Write(want)
	})
	c := newTestClient(t, mux)

	got, err := c.FetchDiff(context.Background(), ref)
	if err != nil {
		t.Fatalf("FetchDiff: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("FetchDiff body mismatch:\ngot:  %s\nwant: %s", got, want)
	}
}

func TestFetchPR_errorMapping(t *testing.T) {
	tests := []struct {
		status  int
		fixture string
		wantErr error
	}{
		{status: http.StatusUnauthorized, fixture: "error_401.json", wantErr: ErrUnauthorized},
		{status: http.StatusForbidden, fixture: "error_403.json", wantErr: ErrForbidden},
		{status: http.StatusNotFound, fixture: "error_404.json", wantErr: ErrNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc("/repos/o/r/pulls/1", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				w.Write(readTestdata(t, tt.fixture))
			})
			c := newTestClient(t, mux)

			_, err := c.FetchPR(context.Background(), model.PRRef{Owner: "o", Repo: "r", Number: 1})
			if err == nil {
				t.Fatal("FetchPR: want error")
			}
			if !isErr(err, tt.wantErr) {
				t.Errorf("FetchPR error = %v, want wrapping %v", err, tt.wantErr)
			}
		})
	}
}

func isErr(err, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
