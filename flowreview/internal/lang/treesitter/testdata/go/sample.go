package sample

type Greeter struct{ Name string }

func (g *Greeter) Greet() string {
	fn := func() string {
		return "hi " + g.Name
	}
	return fn()
}

func Add(a, b int) int {
	return a + b
}
