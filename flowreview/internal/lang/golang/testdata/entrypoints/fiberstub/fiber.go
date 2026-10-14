// Package fiber is a tiny stand-in for github.com/gofiber/fiber: just enough
// for a func(*fiber.Ctx) error handler to type-check. See ginstub/gin.go for
// why a local stub works for FR-16's matching.
package fiber

// Ctx stands in for fiber.Ctx.
type Ctx struct{}

// Handler stands in for fiber.Handler.
type Handler func(*Ctx) error
