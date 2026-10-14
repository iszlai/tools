// Package gin is a tiny stand-in for github.com/gin-gonic/gin: just enough
// for a func(*gin.Context) handler to type-check. FR-16 matches a gin
// handler by package name and type name, not import path (see
// entrypoints.go's isNamedType), so this local stub is indistinguishable
// from the real module to it.
package gin

// Context stands in for gin.Context.
type Context struct{}

// HandlerFunc stands in for gin.HandlerFunc.
type HandlerFunc func(*Context)
