// Package routes covers FR-16's chi registration heuristic. chistub is
// declared "package chi" (see chistub/chi.go), so it is referred to as chi
// below, not chistub — the real module works the same way.
package routes

import (
	"net/http"

	"fixture.example/entrypoints/chistub"
)

// ChiHandler is registered with chi's router in Mount. It also happens to
// match the plain net/http handler signature, so FR-16 is expected to
// report it twice: once for the signature, once for the registration.
func ChiHandler(w http.ResponseWriter, r *http.Request) {}

// Mount registers ChiHandler the way chi expects.
func Mount(r chi.Router) {
	r.Get("/chi", ChiHandler)
}
