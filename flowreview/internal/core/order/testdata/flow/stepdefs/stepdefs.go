// Package stepdefs is godog-style step definitions for the fixture's
// feature file. ScenarioContext is a tiny stand-in for godog's real type:
// this fixture never runs a real godog suite, it only needs
// gherkin.discoverGoStepDefs's regex to see a "ctx.Given(pattern, handler)"
// call, and the Go loader (unlike gherkin's own fixtures, which live under
// testdata/ and are never type-checked at all) to see the handler really
// calling into the changed service package.
package stepdefs

import "flow.example/app/service"

// ScenarioContext stands in for godog's *godog.ScenarioContext.
type ScenarioContext struct{}

// Given stands in for godog's registration method.
func (ScenarioContext) Given(pattern string, fn func() error) {}

// InitializeScenario registers the step definitions a real godog suite
// would call from its TestMain.
func InitializeScenario(ctx *ScenarioContext) {
	ctx.Given(`^a create user request is validated$`, aCreateUserRequestIsValidated)
}

// aCreateUserRequestIsValidated is unchanged itself; it calls the changed
// service.Validate, so it is the "step def" link in "Scenario -> step def
// -> service".
func aCreateUserRequestIsValidated() error {
	return service.Validate("fixture-user")
}
