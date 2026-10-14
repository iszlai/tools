// Package httphandlers covers FR-16's plain net/http entry-point heuristics:
// the bare handler signature, a type implementing http.Handler, and the
// http.HandleFunc/mux.HandleFunc/http.Handle call-expression scan.
package httphandlers

import "net/http"

// Plain matches the net/http handler signature directly; nothing registers
// it, so only the signature heuristic should find it.
func Plain(w http.ResponseWriter, r *http.Request) {}

// handlerType implements http.Handler via ServeHTTP.
type handlerType struct{}

// ServeHTTP makes handlerType an http.Handler.
func (handlerType) ServeHTTP(w http.ResponseWriter, r *http.Request) {}

// ViaHandleFunc is registered through both http.HandleFunc and a
// *http.ServeMux's HandleFunc method below.
func ViaHandleFunc(w http.ResponseWriter, r *http.Request) {}

// ViaHandle is registered through http.Handle wrapped in http.HandlerFunc.
func ViaHandle(w http.ResponseWriter, r *http.Request) {}

// RegisterRoutes wires up every registration style FR-16's call-expression
// scan should recognise.
func RegisterRoutes(mux *http.ServeMux) {
	http.HandleFunc("/plain", ViaHandleFunc)
	mux.HandleFunc("/mux", ViaHandleFunc)
	http.Handle("/handle", http.HandlerFunc(ViaHandle))
}
