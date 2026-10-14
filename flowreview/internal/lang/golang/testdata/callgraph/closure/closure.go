// Package closure exercises FR-15's attribution rules: a call inside a
// closure, a goroutine or a deferred call is reported as made by the
// function that lexically contains it, and a method value resolves to the
// method it wraps.
package closure

// Helper is called from inside a closure, a goroutine and a deferred call —
// all three must report Helper's caller as the named function containing
// them, never an anonymous function.
func Helper() int { return 1 }

// RunClosure calls Helper from inside an anonymous function; the edge must
// name RunClosure as From, not the closure.
func RunClosure() int {
	f := func() int { return Helper() }
	return f()
}

// RunGoroutine starts Helper as a goroutine.
func RunGoroutine() {
	go Helper()
}

// RunDeferred defers a call to Helper.
func RunDeferred() {
	defer Helper()
}

// Greeter has a method used both directly and as a method value.
type Greeter struct{ name string }

// Greet is Greeter's one method.
func (g Greeter) Greet() string { return "hello " + g.name }

// MethodValue takes g.Greet as a func value and calls it through the local
// variable: the edge's To must still be Greeter.Greet, not a synthetic
// "bound$..." name.
func MethodValue(g Greeter) string {
	f := g.Greet
	return f()
}
