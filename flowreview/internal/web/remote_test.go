package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/iszlai/tools/flowreview/internal/host"
)

func TestHandleRemoteComments_empty(t *testing.T) {
	ws, store := newTestWorkspace(t)
	s := New(store, ws, nil)

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/remote-comments", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var got []host.RemoteComment
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("got %+v, want an empty (non-null) array for a non-PR session", got)
	}
}

func TestHandleRemoteComments_populated(t *testing.T) {
	ws, store := newTestWorkspace(t)
	ws.Remote = []host.RemoteComment{{ID: 1, Author: "reviewer", Body: "nit: typo"}}
	s := New(store, ws, nil)

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/remote-comments", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var got []host.RemoteComment
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].Body != "nit: typo" {
		t.Errorf("got %+v, want the one comment set on the workspace", got)
	}
}
