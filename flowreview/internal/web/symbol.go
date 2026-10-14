// FR-18's call-chain explorer web surface: GET /api/symbol (the function or
// method enclosing a requested line, plus its callers/callees/
// implementations) and GET /api/file/{id}/symbols (every function/method
// declaration line on both sides of one diff file, precomputed so the UI can
// mark decl rows with a nav button without a request per row). Go's exact
// analysis reuses order.go's shared, already-cached *golang.Result
// (orderAnalysisFor) instead of starting a Loader of its own -- the same
// background-analysis pattern radius.go and order.go document, and the
// coordinator those two already call out as the place to converge on.
package web

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"

	"github.com/iszlai/tools/flowreview/internal/core/git"
	"github.com/iszlai/tools/flowreview/internal/core/review"
	"github.com/iszlai/tools/flowreview/internal/core/symbol"
	"github.com/iszlai/tools/flowreview/internal/lang"
	"github.com/iszlai/tools/flowreview/internal/lang/golang"
	"github.com/iszlai/tools/flowreview/internal/lang/treesitter"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// symbolResponse is GET /api/symbol's envelope, matching radiusResponse's
// own {pending, error, report} shape: Go's exact-tier path can be pending
// while the shared background analysis loads.
type symbolResponse struct {
	Pending bool           `json:"pending"`
	Error   string         `json:"error,omitempty"`
	Report  *symbol.Report `json:"report,omitempty"`
}

// handleSymbol serves GET /api/symbol?path=&side=old|new&line=. path is read
// straight from the repository, never looked up against the current diff's
// file list first -- it works for a file outside the diff entirely, which is
// exactly what happens the moment the UI follows a target the read-only
// file view (blob.go) is showing.
func (s *Server) handleSymbol(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	side := r.URL.Query().Get("side")
	if side == "" {
		side = "new"
	}
	line, lineErr := strconv.Atoi(r.URL.Query().Get("line"))
	if path == "" || lineErr != nil || line <= 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("symbol: path and a positive line are required"))
		return
	}
	if side != "old" && side != "new" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("symbol: side must be %q or %q", "old", "new"))
		return
	}

	ws := s.workspace()
	sess, files, repo := ws.Snapshot()
	if repo == nil {
		writeJSON(w, http.StatusOK, symbolResponse{})
		return
	}
	src, err := readSideContent(r.Context(), repo, sess, path, side)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	if lang.Detect(path) == "go" {
		rep, pending, err := s.resolveGoSymbol(r.Context(), ws, files, path, src, line, side == "new")
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, symbolResponse{Pending: pending, Report: rep})
		return
	}

	p := treesitterProviderFor(repo.Root())
	if !p.Supports(path) {
		writeJSON(w, http.StatusOK, symbolResponse{})
		return
	}
	rep, err := symbol.ResolveStructural(r.Context(), p, files, path, src, line)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, symbolResponse{Report: rep})
}

// resolveGoSymbol answers GET /api/symbol for a .go file: exact is whether
// the exact-tier call graph even applies (side == "new" -- see
// symbol.ResolveGo's own doc comment for why the old side never gets one).
// It waits on nothing: orderAnalysisFor starts (or reuses) FR-17's shared
// background Load for ws and this just reads whatever it has so far, so the
// very first call for a Workspace -- whichever of GET /api/order, /api/tests
// or /api/symbol happens to land first -- is the one that actually starts
// it.
func (s *Server) resolveGoSymbol(ctx context.Context, ws *review.Workspace, files []model.ChangedFile, path string, src []byte, line int, exact bool) (*symbol.Report, bool, error) {
	st := orderAnalysisFor(ws)
	if exact && !st.ready {
		return nil, true, nil
	}
	var res *golang.Result
	if st.ready {
		res = st.in.Go
	}
	rep, err := symbol.ResolveGo(ctx, res, files, path, src, line, exact && st.ready)
	return rep, false, err
}

// tsProviders caches one treesitter.Provider per repository root: a
// Provider's own per-file analysis cache (see treesitter.Provider's doc
// comment) is only worth anything if the same instance answers every
// request for a given repo, not a fresh one per GET /api/symbol call.
// Never evicted -- same reasoning as radiusCache and testsStates: one
// flowreview process reviews a small, bounded set of repositories in
// practice.
var (
	tsProvidersMu sync.Mutex
	tsProviders   = map[string]*treesitter.Provider{}
)

func treesitterProviderFor(repoRoot string) *treesitter.Provider {
	tsProvidersMu.Lock()
	defer tsProvidersMu.Unlock()
	p, ok := tsProviders[repoRoot]
	if !ok {
		p = treesitter.New(repoRoot)
		tsProviders[repoRoot] = p
	}
	return p
}

// readSideContent reads path's content at sess's old or new revision -- the
// same revision resolution expand.go's expandContent already works out for
// one model.ChangedFile (expandRevisions, defined there, same package),
// generalized to an arbitrary repo-relative path so GET /api/symbol and
// GET /api/blob can read a navigation target outside the diff's own file
// list.
func readSideContent(ctx context.Context, repo *git.Repo, sess *model.ReviewSession, path, side string) ([]byte, error) {
	oldRev, newRev, newIsWorktree, ok := expandRevisions(ctx, repo, sess.Source)
	if !ok {
		return nil, fmt.Errorf("symbol: cannot resolve revisions for a %q source", sess.Source.Kind)
	}
	if side == "old" {
		return repo.Show(ctx, oldRev, path)
	}
	if newIsWorktree {
		return os.ReadFile(filepath.Join(repo.Root(), path))
	}
	return repo.Show(ctx, newRev, path)
}

// outlineSymbols lists src's top-level func/method declarations (and
// everything else Outline reports -- callers filter to SymFunc/SymMethod
// themselves) for path, picking the provider the same way handleSymbol does:
// golang.Provider's syntax-only Outline for ".go" (no Loader needed, see its
// doc comment), treesitter.Provider's grammar-based one otherwise. A
// language with no compiled grammar returns (nil, nil) -- not every file in
// a diff has declarations FR-18 can name.
func outlineSymbols(ctx context.Context, repoRoot, path string, src []byte) ([]model.CodeSymbol, error) {
	if lang.Detect(path) == "go" {
		return golang.NewProvider(nil).Outline(ctx, path, src)
	}
	p := treesitterProviderFor(repoRoot)
	if !p.Supports(path) {
		return nil, nil
	}
	return p.Outline(ctx, path, src)
}

// declLines filters outlineSymbols' result down to func/method declaration
// start lines, sorted -- exactly what GET /api/file/{id}/symbols and
// GET /api/blob hand the UI to mark rows with a nav button.
func declLines(ctx context.Context, repoRoot, path string, src []byte) []int {
	if len(src) == 0 || path == "" {
		return nil
	}
	syms, err := outlineSymbols(ctx, repoRoot, path, src)
	if err != nil {
		return nil
	}
	out := make([]int, 0, len(syms))
	for _, sym := range syms {
		if sym.Kind == model.SymFunc || sym.Kind == model.SymMethod {
			out = append(out, sym.Range.StartLine)
		}
	}
	sort.Ints(out)
	return out
}

// fileSymbolsView is GET /api/file/{id}/symbols's response.
type fileSymbolsView struct {
	Old []int `json:"old"`
	New []int `json:"new"`
}

// handleFileSymbols serves GET /api/file/{id}/symbols: FR-18's per-file
// declaration-line precompute. Synchronous -- Outline is a single parse, not
// a go/packages Load -- so unlike GET /api/symbol this never answers
// {"pending": true}.
func (s *Server) handleFileSymbols(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess, files, repo := s.workspace().Snapshot()
	file, ok := fileByID(files, id)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Errorf("file %q not found", id))
		return
	}
	if repo == nil {
		writeJSON(w, http.StatusOK, fileSymbolsView{})
		return
	}
	oldSrc, newSrc, err := expandContent(r.Context(), repo, sess, file)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, fileSymbolsView{
		Old: declLines(r.Context(), repo.Root(), file.OldPath, oldSrc),
		New: declLines(r.Context(), repo.Root(), file.Path(), newSrc),
	})
}
