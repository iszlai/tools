// Package echo is a tiny stand-in for github.com/labstack/echo: just enough
// for a func(echo.Context) error handler to type-check. See ginstub/gin.go
// for why a local stub works for FR-16's matching.
package echo

// Context stands in for echo.Context. The real type is an interface, passed
// by value — kept that way here, since FR-16's matcher checks for exactly
// that (see entrypoints.go's classifyHandlerSignature).
type Context interface{}

// HandlerFunc stands in for echo.HandlerFunc.
type HandlerFunc func(Context) error
