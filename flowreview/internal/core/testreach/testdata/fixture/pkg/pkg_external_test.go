// Package pkg_test is the external test package variant: TestExternal can
// only call pkg's exported names, exactly like a real consumer would.
package pkg_test

import (
	"testing"

	"testreach.example/fixture/pkg"
)

func TestExternal(t *testing.T) {
	pkg.ExternalTarget()
}
