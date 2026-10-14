// Package web is flowreview's HTTP API and the embedded web UI it serves: one
// Server reviews one review.Workspace at a time, on 127.0.0.1. A second
// `flowreview` invocation hands its source to the running Server's
// POST /api/open instead of starting a competing one (see cmd/flowreview).
package web

import (
	"embed"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"sync"

	"github.com/iszlai/tools/flowreview/internal/core/render"
	"github.com/iszlai/tools/flowreview/internal/core/review"
	"github.com/iszlai/tools/flowreview/internal/core/session"
	"github.com/iszlai/tools/flowreview/internal/llm/summary"
)

// ui/md.js is mdlite, copied in from /Users/lehel/repos/tools/mdlite/md.js
// the way taskhound and diagme do (see that file's own header comment): embed
// cannot reach outside this directory, so the copy is checked in here.
// ui/summary.js is FR-29's trimmed summary UI, kept out of app.js entirely --
// see its own header comment.
//
//go:embed ui/index.html ui/app.js ui/summary.js ui/styles.css ui/md.js
var uiFiles embed.FS

// uiRoot strips the "ui/" prefix embed.FS keeps, so "/" serves index.html
// and "/app.js" serves ui/app.js.
var uiRoot = func() fs.FS {
	sub, err := fs.Sub(uiFiles, "ui")
	if err != nil {
		panic(err) // the embed directive above guarantees "ui" exists
	}
	return sub
}()

// WindowController is how the server asks the native window to come forward
// and show a different session, without this package knowing anything about
// WKWebView or cgo. internal/window's package-level Focus/Navigate satisfy it
// directly; tests inject a fake.
type WindowController interface {
	Focus()
	Navigate(url string)
}

// noopWindow is the default when there is nothing to focus: --no-open,
// --browser, and tests that do not care.
type noopWindow struct{}

func (noopWindow) Focus()          {}
func (noopWindow) Navigate(string) {}

// Server is flowreview's HTTP API plus the embedded UI.
type Server struct {
	store       *session.Store
	renderCache *render.Cache

	mu        sync.RWMutex
	ws        *review.Workspace
	window    WindowController
	addr      string
	summaries *summary.Pipeline // FR-28; nil until SetSummaries, or forever in tests that don't need it

	mux *http.ServeMux
}

// New builds a Server over ws. window may be nil, which defaults to a no-op
// controller (used when there is no real window, e.g. --no-open or tests);
// production wires it up with SetWindow once a native window exists.
func New(store *session.Store, ws *review.Workspace, window WindowController) *Server {
	if window == nil {
		window = noopWindow{}
	}
	s := &Server{store: store, ws: ws, window: window, renderCache: render.NewCache(), mux: http.NewServeMux()}
	s.routes()
	return s
}

// SetWindow replaces the window controller after construction. The server is
// built (and can start Listen-ing) before main calls window.Run, which is
// the only point a real window exists to hand it.
func (s *Server) SetWindow(w WindowController) {
	if w == nil {
		w = noopWindow{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.window = w
}

// SetSummaries wires up FR-28's summary pipeline, the way SetWindow wires up
// a real window: main.go builds the Pipeline (it needs the user's model
// config, resolved after New) and hands it over once it exists. A Server with
// no Pipeline set answers the summaries endpoints as "everything pending"
// rather than erroring (see handleSummaries).
func (s *Server) SetSummaries(p *summary.Pipeline) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.summaries = p
}

func (s *Server) summariesPipeline() *summary.Pipeline {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.summaries
}

// Handler is the server's http.Handler, for tests that want to drive it with
// httptest.NewServer without going through a real socket.
func (s *Server) Handler() http.Handler { return s.mux }

// Listen binds 127.0.0.1:port (0 picks a free port) and records the bound
// address so URL() reports it. Call Serve on the result to start handling
// requests.
func (s *Server) Listen(port int) (net.Listener, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.addr = ln.Addr().String()
	s.mu.Unlock()
	return ln, nil
}

// Serve blocks, handling requests on ln until it is closed.
func (s *Server) Serve(ln net.Listener) error {
	return http.Serve(ln, s.mux)
}

// URL is the base URL of the last successful Listen, e.g.
// "http://127.0.0.1:54321". Empty until Listen has run.
func (s *Server) URL() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.addr == "" {
		return ""
	}
	return "http://" + s.addr
}

func (s *Server) workspace() *review.Workspace {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ws
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/session", s.handleSession)
	s.mux.HandleFunc("GET /api/files", s.handleFiles)
	s.mux.HandleFunc("GET /api/file/{id}/rows", s.handleFileRows)
	s.mux.HandleFunc("GET /api/file/{id}/expand", s.handleExpandHunk) // FR-8, internal/web/expand.go
	s.mux.HandleFunc("GET /styles/chroma.css", s.handleChromaCSS)
	s.mux.HandleFunc("POST /api/state", s.handleState)
	s.mux.HandleFunc("POST /api/comments", s.handleUpsertComment)
	s.mux.HandleFunc("DELETE /api/comments/{id}", s.handleDeleteComment)
	s.mux.HandleFunc("POST /api/open", s.handleOpen)
	s.mux.HandleFunc("POST /api/reload", s.handleReload)                      // FR-9
	s.mux.HandleFunc("GET /api/remote-comments", s.handleRemoteComments)      // FR-12, internal/web/remote.go
	s.mux.HandleFunc("GET /api/tests", s.handleTests)                         // FR-21, internal/web/tests.go
	s.mux.HandleFunc("GET /api/radius", s.handleRadius)                       // FR-19, internal/web/radius.go
	s.mux.HandleFunc("GET /api/order", s.handleOrder)                         // FR-17, internal/web/order.go
	s.mux.HandleFunc("POST /api/publish", s.handlePublish)                    // FR-11, internal/web/publish.go
	s.mux.HandleFunc("POST /api/publish/preflight", s.handlePublishPreflight) // FR-11, internal/web/publish.go
	s.mux.HandleFunc("GET /api/summaries", s.handleSummaries)                 // FR-28, internal/web/summary.go
	s.mux.HandleFunc("POST /api/summaries", s.handleSummaries)                // FR-28, internal/web/summary.go
	s.mux.HandleFunc("POST /api/summaries/regenerate", s.handleRegenerateSummary) // FR-28, internal/web/summary.go
	s.mux.Handle("/", http.FileServer(http.FS(uiRoot)))
}
