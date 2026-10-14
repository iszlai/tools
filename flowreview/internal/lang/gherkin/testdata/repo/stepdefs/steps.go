// Package stepdefs is godog-style step definitions, fixture data for
// gherkin package tests. It lives under testdata/ so the Go toolchain never
// builds or vets it as part of the module.
package stepdefs

import "github.com/cucumber/godog"

// InitializeScenario registers the step definitions a real godog suite
// would call from its TestMain.
func InitializeScenario(ctx *godog.ScenarioContext) {
	ctx.Step(`^a user named "([^"]*)" exists$`, aUserNamedExists)
	ctx.Step(`^they see "([^"]*)"$`, theySeeGreeting)
}

func aUserNamedExists(name string) error {
	return nil
}

func theySeeGreeting(msg string) error {
	return nil
}
