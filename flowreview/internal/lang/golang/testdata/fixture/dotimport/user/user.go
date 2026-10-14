// Package user dot-imports lib so FR-14's Definition test can check that an
// identifier introduced by a dot import still resolves to its real
// declaration in the other package, not to nothing.
package user

import . "fixture.example/flowreviewtest/dotimport/lib"

// UseWidget calls the dot-imported Widget.
func UseWidget() int { return Widget() }
