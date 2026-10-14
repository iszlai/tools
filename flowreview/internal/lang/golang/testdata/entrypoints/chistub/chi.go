// Package chi is a tiny stand-in for github.com/go-chi/chi: just enough
// surface for an r.Get/r.Post registration call to type-check. chi's
// handlers are plain net/http handlers (no framework-specific context type),
// so FR-16 recognises them by the registration call site, not by signature
// — see entrypoints.go's httpRegistrationEntryPoints.
package chi

import "net/http"

// Router stands in for chi.Router.
type Router interface {
	Get(pattern string, h http.HandlerFunc)
	Post(pattern string, h http.HandlerFunc)
}

type router struct{}

// NewRouter stands in for chi.NewRouter.
func NewRouter() Router { return &router{} }

func (*router) Get(pattern string, h http.HandlerFunc)  {}
func (*router) Post(pattern string, h http.HandlerFunc) {}
