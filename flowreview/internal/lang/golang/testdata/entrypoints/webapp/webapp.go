// Package webapp covers FR-16's gin, echo and fiber handler-signature
// heuristics. Each stub package below lives in a directory named ...stub
// but is declared "package gin"/"package echo"/"package fiber" — so, per
// Go's import rules, it is referred to by that declared name, not the
// directory name, exactly like the real third-party modules would be.
package webapp

import (
	"fixture.example/entrypoints/echostub"
	"fixture.example/entrypoints/fiberstub"
	"fixture.example/entrypoints/ginstub"
)

// GinHandler matches gin's handler signature.
func GinHandler(c *gin.Context) {}

// EchoHandler matches echo's handler signature.
func EchoHandler(c echo.Context) error { return nil }

// FiberHandler matches fiber's handler signature.
func FiberHandler(c *fiber.Ctx) error { return nil }
