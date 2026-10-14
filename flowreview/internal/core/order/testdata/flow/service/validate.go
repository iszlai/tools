package service

import "errors"

// Validate is changed, and is reached only from stepdefs.aCreateUserIsValidated
// (see ../stepdefs/stepdefs.go) -- the fixture's "Scenario -> step def ->
// service" chain.
func Validate(name string) error {
	if name == "" {
		return errors.New("empty name")
	}
	return nil
}
