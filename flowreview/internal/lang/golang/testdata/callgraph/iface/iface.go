// Package iface exercises FR-15's interface-dispatch call edges and
// implementations: Speak is called through the Speaker interface, so
// VTA/CHA must resolve Announce's call site to every concrete implementer in
// this fixture — Dog and Cat — never pick one.
package iface

// Speaker can speak.
type Speaker interface {
	Speak() string
}

// Dog implements Speaker with a value receiver.
type Dog struct{}

func (Dog) Speak() string { return "woof" }

// Cat implements Speaker with a pointer receiver.
type Cat struct{}

func (*Cat) Speak() string { return "meow" }

// Announce calls s.Speak() through the interface: an interface-dispatch call
// site with two static candidates in this fixture.
func Announce(s Speaker) string { return s.Speak() }

// Make returns a Dog or a Cat, so both concrete types are actually
// instantiated somewhere a type-flow analysis can see — an interface
// implemented but never constructed would not appear in VTA's candidate set
// for Announce.
func Make(name string) Speaker {
	if name == "cat" {
		return &Cat{}
	}
	return Dog{}
}

// Demo calls Announce with both concrete implementers. VTA (unlike CHA) only
// considers a concrete type reachable at an interface call site if an actual
// assignment connects them somewhere in the program; declaring Speaker, Dog
// and Cat alone is not enough. These two calls are that connection.
func Demo() {
	Announce(Dog{})
	Announce(&Cat{})
}
