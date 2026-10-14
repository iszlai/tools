// FR-15's tests for Provider.Callers/Callees/Implementations: provider_test.go
// (FR-14) only covers the syntax-only methods and the pre-FR-15 ErrUnsupported
// stubs, which this slice does not change the behavior of (a nil loader still
// means ErrUnsupported — see resolve's doc comment) — so those tests are left
// untouched, and the new functional behavior gets its own file.
package golang

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/iszlai/tools/flowreview/internal/lang"
	"github.com/iszlai/tools/flowreview/internal/model"
)

// TestProvider_CallersReconcilesStructuralID is FR-15's "reconcile FR-14's two
// ID tiers" case: FunctionAt only ever had syntax, so the CodeSymbol it
// returns for Top carries the structural-tier ID "direct/direct.go#Top" (see
// Provider's doc comment), not the exact-tier "...direct.Top" the call graph
// is keyed by. Provider.Callees must still answer correctly by reconciling
// through the symbol's Path and Range.StartLine (resolve, via SymbolAt).
func TestProvider_CallersReconcilesStructuralID(t *testing.T) {
	dir := callgraphFixtureDir(t)
	l := &Loader{Dir: dir}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	l.Warm(ctx, []string{"direct/direct.go"})

	p := NewProvider(l)

	src, err := os.ReadFile(filepath.Join(dir, "direct", "direct.go"))
	if err != nil {
		t.Fatal(err)
	}
	sym, err := p.FunctionAt(ctx, "direct/direct.go", src, 14) // func Top() int { ... }
	if err != nil {
		t.Fatalf("FunctionAt: %v", err)
	}
	if sym == nil || sym.Name != "Top" {
		t.Fatalf("FunctionAt(line 14) = %+v, want Top", sym)
	}
	if sym.ID != "direct/direct.go#Top" {
		t.Fatalf("sym.ID = %q, want the structural-tier form", sym.ID)
	}

	callees, err := p.Callees(ctx, *sym)
	if err != nil {
		t.Fatalf("Callees: %v", err)
	}
	want := modPath + "/direct.Middle"
	found := false
	for _, e := range callees {
		if e.To == want {
			found = true
		}
	}
	if !found {
		t.Errorf("Callees(%+v) = %+v, missing edge to %s", sym, callees, want)
	}
}

// TestProvider_NilLoaderStillUnsupported pins down that FR-15 does not change
// provider_test.go's TestProvider_Unsupported behavior: a Provider built with
// a nil Loader (the FunctionAt/Outline-only construction) has no Result to
// query and must still report ErrUnsupported, not panic.
func TestProvider_NilLoaderStillUnsupported(t *testing.T) {
	p := NewProvider(nil)
	sym := model.CodeSymbol{ID: "x.go#Foo"}
	ctx := context.Background()
	if _, err := p.Callers(ctx, sym); err != lang.ErrUnsupported {
		t.Errorf("Callers err = %v, want ErrUnsupported", err)
	}
	if _, err := p.Callees(ctx, sym); err != lang.ErrUnsupported {
		t.Errorf("Callees err = %v, want ErrUnsupported", err)
	}
	if _, err := p.Implementations(ctx, sym); err != lang.ErrUnsupported {
		t.Errorf("Implementations err = %v, want ErrUnsupported", err)
	}
}
