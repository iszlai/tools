// Package order implements FR-17's flow ordering: ordering a diff's changed
// files the way a reviewer would read them -- from the entry point a change
// is reached from down through the call chain to where it actually lives --
// plus three simpler alternative orders and a stub for FR-23's commit order.
//
// Every strategy takes the same Inputs and returns the same Order shape, so
// a caller (the CLI's `flowreview order`, the HTTP API, a future UI) can
// switch between them without knowing which fields a given Kind actually
// reads. Go analysis (golang.Result) and the Gherkin index are both
// optional: Build always returns *something* from Files alone, improving as
// more analysis becomes available -- a session can show file order
// immediately and upgrade to flow order once FR-13's background Load
// finishes.
package order

import (
	"errors"
	"fmt"

	"github.com/iszlai/tools/flowreview/internal/lang/gherkin"
	"github.com/iszlai/tools/flowreview/internal/lang/golang"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// Kind names one ordering strategy -- the "?kind=" value of the web API
// (internal/web/order.go) and the CLI's positional/flag argument
// (cmd/flowreview/order.go).
const (
	KindFlow      = "flow"
	KindAlpha     = "alpha"
	KindDirectory = "dir"
	KindDiff      = "diff"
	KindCommits   = "commits" // FR-23
)

// ErrUnsupported is Commits' result until FR-23 builds it.
var ErrUnsupported = errors.New("order: not implemented in this slice")

// Inputs is everything an ordering strategy may read. Files is the only
// field every Kind needs; the rest are nil-able, and Flow in particular
// still returns a usable (just flatter) Order when Go or Gherkin analysis
// is not ready yet, or not present in this diff at all.
type Inputs struct {
	Files []model.ChangedFile

	// Go is FR-13/14/15's load result for the diff's changed Go files and
	// their in-module callers, or nil when there is none yet. GoConfig
	// carries only EntryPointsConfig.Patterns from .flowreview.yaml, if
	// any -- Flow always overwrites GoConfig.HasCallers itself, wiring it
	// to Go's own call graph (see entryPointIndex), so a caller never has
	// to.
	Go       *golang.Result
	GoConfig golang.Config

	// Gherkin is FR-40's step-definition index, or nil when the repo has
	// no .feature files, or it has not been built for this session yet.
	Gherkin *gherkin.Index

	// Hotspot scores a repo-relative path 0..1 (see history.Miner.Hotspot);
	// nil when no history has been mined. A function, not a map, so a
	// caller already holding a miner never has to precompute scores for
	// files Build does not end up asking about.
	Hotspot func(path string) float64
}

// Order is one ordering of a diff's changed files, by Kind. It is
// JSON-serializable and, for a given Inputs, deterministic.
type Order struct {
	Kind         string      `json:"kind"`
	Groups       []Group     `json:"groups,omitempty"`
	Unclassified []FileEntry `json:"unclassified,omitempty"`
}

// Group is one flow: a connected component of the call-graph subgraph Flow
// builds, naming the entry point it is reached from (or the top-most
// changed symbol, when no entry point connects to it). The non-flow Kinds
// never populate Groups -- see each one's doc comment in alt.go.
type Group struct {
	Title      string            `json:"title"`
	EntryPoint *model.CodeSymbol `json:"entry_point,omitempty"`
	Files      []FileEntry       `json:"files"`
}

// FileEntry is one changed file placed by an ordering strategy.
type FileEntry struct {
	FileID  string   `json:"file_id"`
	Path    string   `json:"path"`
	Why     string   `json:"why,omitempty"`
	Symbols []string `json:"symbols,omitempty"`
	Hotspot float64  `json:"hotspot,omitempty"`
}

// Build runs the ordering strategy named by kind.
func Build(kind string, in Inputs) (Order, error) {
	switch kind {
	case KindFlow:
		return Flow(in), nil
	case KindAlpha:
		return Alphabetical(in), nil
	case KindDirectory:
		return Directory(in), nil
	case KindDiff:
		return DiffOrder(in), nil
	case KindCommits:
		return Commits(in)
	default:
		return Order{}, fmt.Errorf("order: unknown kind %q", kind)
	}
}

// hotspotOf is in.Hotspot(path), or 0 when no scorer was given.
func hotspotOf(in Inputs, path string) float64 {
	if in.Hotspot == nil {
		return 0
	}
	return in.Hotspot(path)
}
