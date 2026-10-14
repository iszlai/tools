package web

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/iszlai/tools/flowreview/internal/core/order"
)

func TestHandleOrder_DiffKind(t *testing.T) {
	_, srv := newTestServer(t)

	var o order.Order
	resp := getJSON(t, srv.URL+"/api/order?kind=diff", &o)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if o.Kind != order.KindDiff {
		t.Errorf("Kind = %q, want %q", o.Kind, order.KindDiff)
	}
	if len(o.Unclassified) != 1 || o.Unclassified[0].Path != "a.txt" {
		t.Errorf("Unclassified = %+v, want just a.txt", o.Unclassified)
	}
}

// TestHandleOrder_FlowKind_BackgroundAnalysis checks that "flow" either
// answers {"pending": true} or, once the fixture's (trivially fast, no Go
// files) background analysis finishes, a real flow Order -- never blocking
// the request on the analysis itself.
func TestHandleOrder_FlowKind_BackgroundAnalysis(t *testing.T) {
	_, srv := newTestServer(t)

	deadline := time.Now().Add(5 * time.Second)
	for {
		var raw map[string]json.RawMessage
		resp := getJSON(t, srv.URL+"/api/order?kind=flow", &raw)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d", resp.StatusCode)
		}
		if pending, ok := raw["pending"]; ok && string(pending) == "true" {
			if time.Now().After(deadline) {
				t.Fatal("still pending after 5s")
			}
			time.Sleep(10 * time.Millisecond)
			continue
		}

		var kind struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(raw["kind"], &kind.Kind); err != nil {
			t.Fatalf("decode kind: %v (raw: %v)", err, raw)
		}
		if kind.Kind != order.KindFlow {
			t.Errorf("Kind = %q, want %q", kind.Kind, order.KindFlow)
		}
		return
	}
}
