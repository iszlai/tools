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
)

//go:embed ui/index.html ui/app.js ui/styles.css
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

	mu     sync.RWMutex
	ws     *review.Workspace
	window WindowController
	addr   string

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
	s.mux.HandleFunc("GET /styles/chroma.css", s.handleChromaCSS)
	s.mux.HandleFunc("POST /api/state", s.handleState)
	s.mux.HandleFunc("POST /api/comments", s.handleUpsertComment)
	s.mux.HandleFunc("DELETE /api/comments/{id}", s.handleDeleteComment)
	s.mux.HandleFunc("POST /api/open", s.handleOpen)
	s.mux.HandleFunc("GET /api/remote-comments", s.handleRemoteComments) // FR-12, internal/web/remote.go
	s.mux.Handle("/", http.FileServer(http.FS(uiRoot)))
}
