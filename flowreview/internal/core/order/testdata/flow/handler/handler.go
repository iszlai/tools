// Package handler is the fixture's HTTP entry point: unchanged itself, but
// it calls into the changed service package, so flow ordering should reach
// service.CreateUser through it.
package handler

import (
	"net/http"

	"flow.example/app/service"
)

// CreateUser matches net/http's handler signature, so golang's entry-point
// heuristics find it without any registration call.
func CreateUser(w http.ResponseWriter, r *http.Request) {
	_ = service.CreateUser(r.FormValue("name"))
}
