package golang

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/iszlai/tools/flowreview/internal/model"
)

func callgraphFixtureDir(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("testdata", "callgraph"))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

// loadCallgraphFixture loads every package in testdata/callgraph into one
// Result, by naming one file per package as "changed" (loader.go's scope is
// changed packages plus their in-module reverse importers; none of this
// fixture's packages import each other, so this simply materializes all
// four).
func loadCallgraphFixture(t *testing.T) *Result {
	t.Helper()
	l := &Loader{Dir: callgraphFixtureDir(t)}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := l.Load(ctx, []string{
		"direct/direct.go",
		"iface/iface.go",
		"closure/closure.go",
		"generic/generic.go",
	})
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return res
}

const modPath = "callgraph.example/flowreviewtest"

func hasEdge(edges []model.NavigationEdge, from, to string) bool {
	for _, e := range edges {
		if e.From == from && e.To == to {
			return true
		}
	}
	return false
}

func TestCallees_DirectStaticCalls(t *testing.T) {
	res := loadCallgraphFixture(t)

	top := modPath + "/direct.Top"
	middle := modPath + "/direct.Middle"
	leaf := modPath + "/direct.Leaf"

	callees, err := Callees(res, top)
	if err != nil {
		t.Fatalf("Callees: %v", err)
	}
	if len(callees) != 1 {
		t.Fatalf("Callees(Top) = %+v, want exactly 1 edge", callees)
	}
	e := callees[0]
	if e.Kind != model.EdgeCalls || e.From != top || e.To != middle {
		t.Errorf("edge = %+v, want Kind=calls From=%s To=%s", e, top, middle)
	}
	if e.Confidence != model.ConfStatic {
		t.Errorf("Confidence = %s, want static", e.Confidence)
	}
	if e.Source != "vta" && e.Source != "cha" {
		t.Errorf("Source = %q, want vta or cha", e.Source)
	}
	if e.Site == nil || e.Site.Path != "direct/direct.go" {
		t.Errorf("Site = %+v, want direct/direct.go", e.Site)
	}

	// Leaf has no outgoing calls.
	leafCallees, err := Callees(res, leaf)
	if err != nil {
		t.Fatalf("Callees(Leaf): %v", err)
	}
	if len(leafCallees) != 0 {
		t.Errorf("Callees(Leaf) = %+v, want none", leafCallees)
	}
}

func TestCallers_DirectStaticCalls(t *testing.T) {
	res := loadCallgraphFixture(t)

	middle := modPath + "/direct.Middle"
	top := modPath + "/direct.Top"

	callers, err := Callers(res, middle)
	if err != nil {
		t.Fatalf("Callers: %v", err)
	}
	if len(callers) != 1 || callers[0].From != top || callers[0].To != middle {
		t.Fatalf("Callers(Middle) = %+v, want exactly one edge from Top", callers)
	}
}

func TestCallees_InterfaceDispatchReturnsAllCandidates(t *testing.T) {
	res := loadCallgraphFixture(t)

	announce := modPath + "/iface.Announce"
	dogSpeak := modPath + "/iface.(Dog).Speak"
	catSpeak := modPath + "/iface.(Cat).Speak"

	callees, err := Callees(res, announce)
	if err != nil {
		t.Fatalf("Callees: %v", err)
	}
	if !hasEdge(callees, announce, dogSpeak) {
		t.Errorf("Callees(Announce) = %+v, missing edge to Dog.Speak", callees)
	}
	if !hasEdge(callees, announce, catSpeak) {
		t.Errorf("Callees(Announce) = %+v, missing edge to Cat.Speak", callees)
	}
	for _, e := range callees {
		if e.Confidence != model.ConfInterfaceDispatch {
			t.Errorf("edge %+v Confidence = %s, want interface-dispatch", e, e.Confidence)
		}
	}
}

func TestClosures_AttributeToEnclosingNamedFunction(t *testing.T) {
	res := loadCallgraphFixture(t)

	helper := modPath + "/closure.Helper"
	runClosure := modPath + "/closure.RunClosure"
	runGoroutine := modPath + "/closure.RunGoroutine"
	runDeferred := modPath + "/closure.RunDeferred"

	callers, err := Callers(res, helper)
	if err != nil {
		t.Fatalf("Callers: %v", err)
	}
	for _, want := range []string{runClosure, runGoroutine, runDeferred} {
		if !hasEdge(callers, want, helper) {
			t.Errorf("Callers(Helper) = %+v, missing edge from %s", callers, want)
		}
	}
	for _, e := range callers {
		if e.Confidence != model.ConfStatic {
			t.Errorf("edge %+v Confidence = %s, want static (a direct call to a named function)", e, e.Confidence)
		}
	}
}

func TestMethodValue_ResolvesToTheMethod(t *testing.T) {
	res := loadCallgraphFixture(t)

	methodValue := modPath + "/closure.MethodValue"
	greet := modPath + "/closure.(Greeter).Greet"

	callees, err := Callees(res, methodValue)
	if err != nil {
		t.Fatalf("Callees: %v", err)
	}
	if !hasEdge(callees, methodValue, greet) {
		t.Errorf("Callees(MethodValue) = %+v, missing edge to Greeter.Greet", callees)
	}
}

func TestGenerics_InstantiationsAttributeToOrigin(t *testing.T) {
	res := loadCallgraphFixture(t)

	max := modPath + "/generic.Max"
	callInt := modPath + "/generic.CallInt"
	callString := modPath + "/generic.CallString"

	callers, err := Callers(res, max)
	if err != nil {
		t.Fatalf("Callers: %v", err)
	}
	if !hasEdge(callers, callInt, max) {
		t.Errorf("Callers(Max) = %+v, missing edge from CallInt", callers)
	}
	if !hasEdge(callers, callString, max) {
		t.Errorf("Callers(Max) = %+v, missing edge from CallString", callers)
	}
	if len(callers) != 2 {
		t.Errorf("Callers(Max) = %+v, want exactly 2 edges (one per instantiation site), not one per type argument", callers)
	}
}

func TestImplementations_InterfaceToConcreteTypes(t *testing.T) {
	res := loadCallgraphFixture(t)

	speaker := modPath + "/iface.Speaker"
	dog := modPath + "/iface.Dog"
	cat := modPath + "/iface.Cat"

	impls, err := Implementations(res, speaker)
	if err != nil {
		t.Fatalf("Implementations: %v", err)
	}
	if !hasEdge(impls, dog, speaker) {
		t.Errorf("Implementations(Speaker) = %+v, missing Dog", impls)
	}
	if !hasEdge(impls, cat, speaker) {
		t.Errorf("Implementations(Speaker) = %+v, missing Cat", impls)
	}
	for _, e := range impls {
		if e.Kind != model.EdgeImplements || e.Confidence != model.ConfStatic || e.Source != "types" {
			t.Errorf("edge %+v, want Kind=implements Confidence=static Source=types", e)
		}
	}
}

func TestImplementations_InterfaceMethodToConcreteMethods(t *testing.T) {
	res := loadCallgraphFixture(t)

	speakMethod := modPath + "/iface.(Speaker).Speak"
	dogSpeak := modPath + "/iface.(Dog).Speak"
	catSpeak := modPath + "/iface.(Cat).Speak"

	impls, err := Implementations(res, speakMethod)
	if err != nil {
		t.Fatalf("Implementations: %v", err)
	}
	if !hasEdge(impls, dogSpeak, speakMethod) {
		t.Errorf("Implementations(Speaker.Speak) = %+v, missing Dog.Speak", impls)
	}
	if !hasEdge(impls, catSpeak, speakMethod) {
		t.Errorf("Implementations(Speaker.Speak) = %+v, missing Cat.Speak", impls)
	}
}

func TestImplementations_ConcreteMethodListsSatisfiedInterfaces(t *testing.T) {
	res := loadCallgraphFixture(t)

	dogSpeak := modPath + "/iface.(Dog).Speak"
	speakMethod := modPath + "/iface.(Speaker).Speak"

	impls, err := Implementations(res, dogSpeak)
	if err != nil {
		t.Fatalf("Implementations: %v", err)
	}
	if !hasEdge(impls, dogSpeak, speakMethod) {
		t.Errorf("Implementations(Dog.Speak) = %+v, missing reverse edge to Speaker.Speak", impls)
	}
}

func TestImplementations_ConcreteTypeListsSatisfiedInterfaces(t *testing.T) {
	res := loadCallgraphFixture(t)

	dog := modPath + "/iface.Dog"
	speaker := modPath + "/iface.Speaker"

	impls, err := Implementations(res, dog)
	if err != nil {
		t.Fatalf("Implementations: %v", err)
	}
	if !hasEdge(impls, dog, speaker) {
		t.Errorf("Implementations(Dog) = %+v, missing reverse edge to Speaker", impls)
	}
}
