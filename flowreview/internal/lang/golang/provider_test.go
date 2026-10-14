package golang

import (
	"context"
	"testing"

	"github.com/iszlai/tools/flowreview/internal/lang"
	"github.com/iszlai/tools/flowreview/internal/model"
)

func TestProvider_Capabilities(t *testing.T) {
	p := NewProvider(nil)
	caps := p.Capabilities()
	if caps.Tier != lang.TierExact {
		t.Errorf("Tier = %v, want TierExact", caps.Tier)
	}
	if len(caps.Languages) != 1 || caps.Languages[0] != "go" {
		t.Errorf("Languages = %v, want [go]", caps.Languages)
	}
	if !caps.Definitions {
		t.Error("Definitions = false, want true")
	}
}

func TestProvider_Supports(t *testing.T) {
	p := NewProvider(nil)
	if !p.Supports("foo/bar.go") {
		t.Error("Supports(foo/bar.go) = false, want true")
	}
	if p.Supports("foo/bar.java") {
		t.Error("Supports(foo/bar.java) = true, want false")
	}
}

func TestProvider_FunctionAt(t *testing.T) {
	p := NewProvider(nil)
	src := []byte("package x\n\nfunc Foo() int { return 1 }\n\nfunc Bar() int { return 2 }\n")
	ctx := context.Background()

	sym, err := p.FunctionAt(ctx, "x.go", src, 3)
	if err != nil {
		t.Fatalf("FunctionAt: %v", err)
	}
	if sym == nil || sym.Name != "Foo" {
		t.Fatalf("FunctionAt(line 3) = %+v, want Foo", sym)
	}
	if sym.ID != "x.go#Foo" {
		t.Errorf("ID = %q, want %q", sym.ID, "x.go#Foo")
	}

	sym, err = p.FunctionAt(ctx, "x.go", src, 1)
	if err != nil {
		t.Fatalf("FunctionAt: %v", err)
	}
	if sym != nil {
		t.Errorf("FunctionAt(line 1) = %+v, want nil", sym)
	}
}

func TestProvider_Outline(t *testing.T) {
	p := NewProvider(nil)
	src := []byte("package x\n\nfunc Foo() int { return 1 }\n\nfunc Bar() int { return 2 }\n")
	out, err := p.Outline(context.Background(), "x.go", src)
	if err != nil {
		t.Fatalf("Outline: %v", err)
	}
	if len(out) != 2 || out[0].Name != "Foo" || out[1].Name != "Bar" {
		t.Fatalf("Outline = %+v, want [Foo Bar]", out)
	}
}

func TestProvider_Unsupported(t *testing.T) {
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
