// Package broken imports a (so it is swept into the scope of a change to a)
// and an import that does not exist, so loading it always fails. The loader
// must still return a and b cleanly and report broken's failure in
// Result.Errors instead of aborting.
package broken

import (
	"fixture.example/flowreviewtest/a"
	"fixture.example/flowreviewtest/doesnotexist"
)

func UseBroken() int {
	doesnotexist.Nope()
	return a.Foo()
}
