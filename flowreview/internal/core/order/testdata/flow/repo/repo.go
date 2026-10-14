// Package repo is the fixture's bottom tier: Save (changed) is reached only
// from service.CreateUser.
package repo

// Save is changed: flow ordering should place it after service.CreateUser,
// the only thing that calls it.
func Save(name string) error {
	return nil
}
