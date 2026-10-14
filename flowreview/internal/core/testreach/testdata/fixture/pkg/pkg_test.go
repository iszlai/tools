package pkg

import "testing"

func TestDirect(t *testing.T) {
	Direct()
}

func TestTransitive(t *testing.T) {
	CallsHelper()
}

func TestInterface(t *testing.T) {
	var d Doer = NewDoer()
	d.Do()
}
