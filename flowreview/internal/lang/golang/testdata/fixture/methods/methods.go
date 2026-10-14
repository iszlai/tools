// Package methods exercises FR-14's receiver-method symbol construction: one
// value receiver and one pointer receiver on the same type.
package methods

// Counter is a tiny counter with both a value-receiver reader and a
// pointer-receiver mutator, so FR-14's tests can check both receiver shapes
// produce the same bare-name ID with a different Receiver field.
type Counter struct {
	n int
}

// Value returns the current count. Value receiver: Receiver should be
// "Counter" (no star) and the ID should still be "...methods.(Counter).Value".
func (c Counter) Value() int { return c.n }

// Add increments the counter. Pointer receiver: Receiver should be
// "*Counter"; the ID has no star, "...methods.(Counter).Add".
func (c *Counter) Add(n int) { c.n += n }
