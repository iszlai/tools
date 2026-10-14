package stepdefs

// stepRegistrar is a thin sugar wrapper some godog suites write over
// ScenarioContext to get Given/When/Then names instead of the generic Step.
type stepRegistrar struct{}

func (sc *stepRegistrar) Given(pattern string, fn func() error) {}

// RegisterMore registers a step definition that deliberately collides with
// one in steps.js: both match "something ambiguous happens", exercising the
// ambiguous-match path in Link.
func RegisterMore(sc *stepRegistrar) {
	sc.Given(`^something ambiguous happens$`, ambiguousOne)
}

func ambiguousOne() error {
	return nil
}
