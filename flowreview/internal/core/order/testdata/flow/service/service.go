// Package service is the fixture's middle tier: CreateUser (changed) is
// reached from the HTTP handler and calls into the changed repo package;
// Validate (in validate.go, also changed) is reached only from the Gherkin
// step definition.
package service

import "flow.example/app/repo"

// CreateUser is changed: flow ordering should place it between the handler
// that calls it and the repo function it calls.
func CreateUser(name string) error {
	return repo.Save(name)
}
