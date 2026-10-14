package d

import "testing"

func TestQux(t *testing.T) {
	if Qux() != 7 {
		t.Fatal("Qux() != 7")
	}
}
