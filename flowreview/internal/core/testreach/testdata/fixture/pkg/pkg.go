// Package pkg is testreach_test.go's fixture: a handful of functions placed
// so each of FR-21's accept criteria has exactly one function to exercise it
// (see pkg_test.go and pkg_external_test.go for the tests that reach them).
package pkg

// Direct is called directly by TestDirect: one hop, a static call.
func Direct() {}

// CallsHelper is unchanged plumbing a test calls; it calls TransitiveTarget
// so that TestTransitive's path to TransitiveTarget is two hops.
func CallsHelper() { TransitiveTarget() }

// TransitiveTarget is reached transitively: TestTransitive -> CallsHelper ->
// TransitiveTarget.
func TransitiveTarget() {}

// Doer is implemented by Impl; NewDoer hands TestInterface a Doer value so
// its call to Do() is an interface dispatch, not a static call.
type Doer interface {
	Do()
}

// Impl is Doer's only implementation in this fixture.
type Impl struct{}

// Do is reached only through interface dispatch: TestInterface holds a Doer,
// never an Impl, so the only edge the call graph can draw to Do has
// Confidence interface-dispatch.
func (Impl) Do() {}

// NewDoer returns a Doer backed by Impl, so TestInterface never names Impl.
func NewDoer() Doer { return Impl{} }

// Unreached has no caller anywhere in this fixture, by any test or
// otherwise.
func Unreached() {}

// ExternalTarget is called only from pkg_external_test.go's external "_test"
// package test.
func ExternalTarget() {}
