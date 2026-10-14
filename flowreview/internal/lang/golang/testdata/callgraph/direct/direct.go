// Package direct exercises FR-15's static call edges: Top calls Middle calls
// Leaf, each resolved at compile time with no interface or dynamic dispatch
// involved.
package direct

// Leaf has no outgoing calls — direct's only function with an empty Callees
// list.
func Leaf() int { return 1 }

// Middle calls Leaf directly.
func Middle() int { return Leaf() + 1 }

// Top calls Middle directly.
func Top() int { return Middle() + 1 }
