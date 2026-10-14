package testkinds

import (
	"fmt"
	"testing"
)

func TestDouble(t *testing.T) {
	if Double(2) != 4 {
		t.Fatal("Double(2) != 4")
	}
}

func BenchmarkDouble(b *testing.B) {
	for i := 0; i < b.N; i++ {
		Double(i)
	}
}

func ExampleDouble() {
	fmt.Println(Double(3))
	// Output: 6
}

func FuzzDouble(f *testing.F) {
	f.Add(2)
	f.Fuzz(func(t *testing.T, n int) {
		Double(n)
	})
}
