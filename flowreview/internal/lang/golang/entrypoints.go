// Entry points: the symbols FR-17's flow ordering treats as roots — places
// outside the loaded scope's own call graph that something starts running
// from. Everything here reads a *Result FR-13 already produced, the same way
// symbols.go does; nothing here triggers a Load.
//
// Every heuristic below is exactly that — a heuristic, not a proof. A
// function matching the net/http handler signature might never actually be
// registered with a mux; a call to a method named Handle might have nothing
// to do with net/http. Matching by package *name* and type *name* rather
// than by full import path (see isNamedType) makes the false-positive risk
// slightly worse in exchange for working against a repo's own vendored or
// hand-rolled stand-ins for gin/echo/fiber, and against this package's own
// test fixture, which declares tiny local stub packages literally named
// "gin", "echo", "fiber" and "cobra" instead of depending on the real
// modules. Each EntryPoint's Reason says exactly which heuristic fired, so a
// caller can judge the confidence for itself.
package golang

import (
	"fmt"
	"go/ast"
	"go/types"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"

	"github.com/iszlai/tools/flowreview/internal/model"
)

// Kind values EntryPoints reports. Kind groups heuristics that answer the
// same practical question ("is this where an HTTP request starts?") even
// when the framework or exact signal differs; Reason carries the detail.
const (
	KindMain         = "main"
	KindInit         = "init"
	KindHTTPHandler  = "http-handler"
	KindGRPCMethod   = "grpc-method"
	KindCobraCommand = "cobra-command"
	KindTest         = "test"
	KindUnreferenced = "unreferenced"
	KindCustom       = "custom"
)

// EntryPoint is one symbol EntryPoints found, with the human-readable reason
// a reviewer (or FR-17's flow ordering) can show for why it is a root.
type EntryPoint struct {
	Symbol model.CodeSymbol
	Kind   string
	Reason string
}

// EntryPoints finds every entry point in res's loaded scope: main, init,
// test functions, web-framework handlers, gRPC service methods, cobra
// command actions, cfg's own patterns, and — only when cfg.HasCallers is
// set, because that needs FR-15's call graph, not yet merged when this slice
// was built — exported symbols nothing in the module calls.
//
// A symbol can appear more than once, under different Kind/Reason pairs,
// when more than one heuristic fires for it (a chi-registered handler, for
// instance, also has the plain net/http signature). That is deliberate: each
// match is independent evidence, and FR-17 only needs "is this a root", not
// a single canonical reason.
func EntryPoints(res *Result, cfg Config) []EntryPoint {
	if res == nil {
		return nil
	}

	byID := make(map[string]model.CodeSymbol)
	for _, s := range Symbols(res) {
		byID[s.ID] = s
	}
	methodsByType := methodsByReceiver(res)

	var out []EntryPoint
	classified := map[string]bool{}
	add := func(sym model.CodeSymbol, kind, reason string) {
		out = append(out, EntryPoint{Symbol: sym, Kind: kind, Reason: reason})
		classified[sym.ID] = true
	}

	seen := map[*packages.Package]bool{}
	for _, p := range res.FileToPkg {
		if seen[p] || p.Types == nil {
			continue
		}
		seen[p] = true

		ifaces := grpcServerInterfaces(p)

		for _, file := range p.Syntax {
			filename := res.Fset.Position(file.Pos()).Filename
			if res.FileToPkg[filename] != p {
				continue // see symbolsForPackage: a shared file belongs to one package variant only
			}
			isTestFile := strings.HasSuffix(filename, "_test.go")

			for _, d := range declsFromFile(file) {
				if d.fnType == nil {
					continue // a type/interface decl; entry points are always executable code
				}
				sym, ok := byID[d.id(p.PkgPath, false)]
				if !ok {
					continue
				}

				if isTestFile && d.recv == "" {
					if reason, ok := testReason(d.name); ok {
						add(sym, KindTest, reason)
					}
				}
				if d.recv == "" && d.name == "main" && p.Name == "main" {
					add(sym, KindMain, "package main's main function")
				}
				if d.recv == "" && d.name == "init" {
					add(sym, KindInit, "init function, run automatically before main")
				}

				fn, _ := defObject(p, d).(*types.Func)
				if fn == nil {
					continue
				}
				sig, _ := fn.Type().(*types.Signature)
				if sig == nil {
					continue
				}
				if kind, reason, ok := classifyHandlerSignature(sig); ok {
					add(sym, kind, reason)
				}
				for _, pat := range cfg.EntryPoints.Patterns {
					if matchesPattern(pat, d.name, sig, p.Types) {
						add(sym, KindCustom, pat.Reason)
					}
				}
			}

			for _, ep := range httpRegistrationEntryPoints(p, file, byID) {
				add(ep.Symbol, ep.Kind, ep.Reason)
			}
			for _, ep := range cobraEntryPoints(p, file, byID) {
				add(ep.Symbol, ep.Kind, ep.Reason)
			}
		}

		for _, ep := range grpcEntryPoints(p, ifaces, methodsByType[p.PkgPath]) {
			add(ep.Symbol, ep.Kind, ep.Reason)
		}
	}

	if cfg.HasCallers != nil {
		for _, s := range Symbols(res) {
			if s.Kind != model.SymFunc && s.Kind != model.SymMethod {
				continue
			}
			if !s.Exported || classified[s.ID] {
				continue
			}
			if cfg.HasCallers(s.ID) {
				continue
			}
			add(s, KindUnreferenced, "exported, no in-module callers")
		}
	}

	return out
}

// testReason classifies a top-level func in a _test.go file by Go's test
// runner naming convention.
func testReason(name string) (string, bool) {
	switch {
	case strings.HasPrefix(name, "Test"):
		return "test function (Test*)", true
	case strings.HasPrefix(name, "Benchmark"):
		return "benchmark function (Benchmark*)", true
	case strings.HasPrefix(name, "Example"):
		return "example function (Example*)", true
	case strings.HasPrefix(name, "Fuzz"):
		return "fuzz function (Fuzz*)", true
	}
	return "", false
}

// classifyHandlerSignature recognises the handler signatures of net/http and
// three popular web frameworks, matched structurally (parameter/result
// shape plus the named types' package and type name — see isNamedType) so
// the real third-party packages and this package's test-fixture stubs are
// indistinguishable to it.
func classifyHandlerSignature(sig *types.Signature) (kind, reason string, ok bool) {
	params, results := sig.Params(), sig.Results()

	switch {
	case params.Len() == 2 && results.Len() == 0 &&
		isNamedType(params.At(0).Type(), false, "http", "ResponseWriter") &&
		isNamedType(params.At(1).Type(), true, "http", "Request"):
		return KindHTTPHandler, "HTTP handler: signature func(http.ResponseWriter, *http.Request)", true

	case params.Len() == 1 && results.Len() == 0 &&
		isNamedType(params.At(0).Type(), true, "gin", "Context"):
		return KindHTTPHandler, "gin handler: signature func(*gin.Context)", true

	case params.Len() == 1 && results.Len() == 1 && isErrorType(results.At(0).Type()) &&
		isNamedType(params.At(0).Type(), false, "echo", "Context"):
		return KindHTTPHandler, "echo handler: signature func(echo.Context) error", true

	case params.Len() == 1 && results.Len() == 1 && isErrorType(results.At(0).Type()) &&
		isNamedType(params.At(0).Type(), true, "fiber", "Ctx"):
		return KindHTTPHandler, "fiber handler: signature func(*fiber.Ctx) error", true
	}
	return "", "", false
}

// isNamedType reports whether t is, modulo one pointer indirection (required
// to be present iff wantPtr), a *types.Named declared as pkgName.typeName.
// Matching by package name and type name rather than full import path is
// deliberate — see this file's doc comment.
func isNamedType(t types.Type, wantPtr bool, pkgName, typeName string) bool {
	ptr, isPtr := t.(*types.Pointer)
	if isPtr != wantPtr {
		return false
	}
	if isPtr {
		t = ptr.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok || named.Obj().Pkg() == nil {
		return false
	}
	return named.Obj().Pkg().Name() == pkgName && named.Obj().Name() == typeName
}

// isErrorType reports whether t is the predeclared error interface. error is
// a universe-scope singleton, so comparing its String() form ("error") is
// sufficient and avoids importing go/types' internal universe accessors.
func isErrorType(t types.Type) bool {
	return t.String() == "error"
}

// matchesPattern reports whether pat (from .flowreview.yaml) matches a
// func/method named name with signature sig declared in pkg. A pattern with
// both FuncRegex and Signature set must match both; one with neither never
// matches.
func matchesPattern(pat EntryPointPattern, name string, sig *types.Signature, pkg *types.Package) bool {
	matched := false
	if pat.compiled != nil {
		if !pat.compiled.MatchString(name) {
			return false
		}
		matched = true
	}
	if pat.Signature != "" {
		if signatureShape(sig, pkg) != pat.Signature {
			return false
		}
		matched = true
	}
	return matched
}

// signatureShape renders sig the way a person writes it in .flowreview.yaml:
// parameter and result types only, no parameter names, package-qualified
// relative to pkg so a type pkg itself declares prints unqualified (matching
// go/types' own convention for types.RelativeTo). This differs from
// CodeSymbol.Signature (symbols.go), which keeps go/types' default parameter
// names — a config author has no reason to know or match those.
func signatureShape(sig *types.Signature, pkg *types.Package) string {
	q := types.RelativeTo(pkg)
	params := make([]string, sig.Params().Len())
	for i := range params {
		params[i] = types.TypeString(sig.Params().At(i).Type(), q)
	}
	switch sig.Results().Len() {
	case 0:
		return fmt.Sprintf("func(%s)", strings.Join(params, ", "))
	case 1:
		return fmt.Sprintf("func(%s) %s", strings.Join(params, ", "), types.TypeString(sig.Results().At(0).Type(), q))
	default:
		results := make([]string, sig.Results().Len())
		for i := range results {
			results[i] = types.TypeString(sig.Results().At(i).Type(), q)
		}
		return fmt.Sprintf("func(%s) (%s)", strings.Join(params, ", "), strings.Join(results, ", "))
	}
}

// resolveFuncObject finds the *types.Func a handler/action argument
// expression denotes: a bare identifier (a top-level func, possibly
// qualified with a package name) or a method value (recv.Method, not a
// call). A qualified identifier's resolved object lives in TypesInfo.Uses;
// a method value's lives in TypesInfo.Selections (go/types only populates
// Uses for *qualified identifiers*, and a method value's selector is not
// one — see go/types.Info's doc comment). Anything else (a function
// literal, a more complex expression) is not resolved: it has no stable ID
// to report.
func resolveFuncObject(p *packages.Package, expr ast.Expr) types.Object {
	switch e := expr.(type) {
	case *ast.Ident:
		return p.TypesInfo.Uses[e]
	case *ast.SelectorExpr:
		if obj := p.TypesInfo.Uses[e.Sel]; obj != nil {
			return obj
		}
		if sel, ok := p.TypesInfo.Selections[e]; ok {
			return sel.Obj()
		}
	}
	return nil
}

// resolveFuncSymbol resolves expr (see resolveFuncObject) to the
// already-computed CodeSymbol for the function or method it denotes, via
// byID — built once by EntryPoints from Symbols(res), so this never
// recomputes a symbol's shape, it only looks one up by the same goID the
// rest of the package uses.
func resolveFuncSymbol(expr ast.Expr, p *packages.Package, byID map[string]model.CodeSymbol) (model.CodeSymbol, bool) {
	obj := resolveFuncObject(p, expr)
	fn, ok := obj.(*types.Func)
	if !ok {
		return model.CodeSymbol{}, false
	}
	recv := ""
	if sig, ok := fn.Type().(*types.Signature); ok && sig.Recv() != nil {
		recv = embeddedTypeName(sig.Recv().Type())
	}
	pkgPath := ""
	if fn.Pkg() != nil {
		pkgPath = fn.Pkg().Path()
	}
	sym, ok := byID[goID(pkgPath, recv, fn.Name())]
	return sym, ok
}

// unwrapHandlerFuncConversion strips a net/http.HandlerFunc(x) conversion
// down to x, so `http.Handle("/", http.HandlerFunc(handler))` resolves to
// handler the same way `http.HandleFunc("/", handler)` does.
func unwrapHandlerFuncConversion(p *packages.Package, expr ast.Expr) ast.Expr {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return expr
	}
	if isNamedType(p.TypesInfo.TypeOf(call.Fun), false, "http", "HandlerFunc") {
		return call.Args[0]
	}
	return expr
}

// httpRegistrationEntryPoints scans file for calls that register an HTTP
// handler by value — http.HandleFunc, mux.HandleFunc, http.Handle, and chi's
// r.Get/Post/... family — and resolves the handler argument to its symbol.
// Matching is on the receiver's package *name* (selectorReceiverPkgName),
// not on a specific receiver variable or import path, so this covers both
// the package-level `http.HandleFunc` and the common `mux.HandleFunc` where
// mux is a *http.ServeMux value: both have a receiver whose type (or, for
// the package-level case, whose package) belongs to the "http" package.
func httpRegistrationEntryPoints(p *packages.Package, file *ast.File, byID map[string]model.CodeSymbol) []EntryPoint {
	var out []EntryPoint
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkgName, ok := selectorReceiverPkgName(p, sel)
		if !ok {
			return true
		}
		argIdx, reason, ok := handlerRegistrationArg(pkgName, sel.Sel.Name)
		if !ok || len(call.Args) <= argIdx {
			return true
		}
		arg := unwrapHandlerFuncConversion(p, call.Args[argIdx])
		if sym, ok := resolveFuncSymbol(arg, p, byID); ok {
			out = append(out, EntryPoint{Symbol: sym, Kind: KindHTTPHandler, Reason: reason})
		}
		return true
	})
	return out
}

// selectorReceiverPkgName is the name of the package sel.X belongs to:
// either the package sel.X itself qualifies (sel.X is an imported package
// identifier, e.g. "http" in http.HandleFunc) or the package that declared
// sel.X's type (e.g. "http" again for a *http.ServeMux variable in
// mux.HandleFunc, "chi" for a chi.Router in r.Get).
func selectorReceiverPkgName(p *packages.Package, sel *ast.SelectorExpr) (string, bool) {
	if ident, ok := sel.X.(*ast.Ident); ok {
		if pn, ok := p.TypesInfo.Uses[ident].(*types.PkgName); ok {
			return pn.Imported().Name(), true
		}
	}
	t := p.TypesInfo.TypeOf(sel.X)
	if t == nil {
		return "", false
	}
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok || named.Obj().Pkg() == nil {
		return "", false
	}
	return named.Obj().Pkg().Name(), true
}

// handlerRegistrationArg says which call argument is the handler for a
// method named method called on a value from package pkgName, and the
// Reason to report for it.
func handlerRegistrationArg(pkgName, method string) (argIdx int, reason string, ok bool) {
	switch {
	case pkgName == "http" && method == "HandleFunc":
		return 1, "HTTP handler: registered via http.HandleFunc", true
	case pkgName == "http" && method == "Handle":
		return 1, "HTTP handler: registered via http.Handle", true
	case pkgName == "chi" && isChiRouteMethod(method):
		return 1, fmt.Sprintf("chi handler: registered via r.%s", method), true
	}
	return 0, "", false
}

func isChiRouteMethod(method string) bool {
	switch method {
	case "Get", "Post", "Put", "Patch", "Delete", "Head", "Options", "Connect", "Trace", "Handle", "HandleFunc":
		return true
	}
	return false
}

// cobraEntryPoints scans file for cobra.Command composite literals and
// resolves their Run/RunE field values to symbols — a pure syntax-plus-types
// scan, since cobra never calls these funcs itself in a way FR-15's call
// graph could see; the command's dispatcher calls them through the Command
// value, not through a static reference this package's call graph can
// follow.
func cobraEntryPoints(p *packages.Package, file *ast.File, byID map[string]model.CodeSymbol) []EntryPoint {
	var out []EntryPoint
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		if !isNamedType(p.TypesInfo.TypeOf(lit.Type), false, "cobra", "Command") {
			return true
		}

		cmdName := ""
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok || key.Name != "Use" {
				continue
			}
			if bl, ok := kv.Value.(*ast.BasicLit); ok {
				if s, err := strconv.Unquote(bl.Value); err == nil {
					if fields := strings.Fields(s); len(fields) > 0 {
						cmdName = fields[0]
					}
				}
			}
		}

		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok || (key.Name != "Run" && key.Name != "RunE") {
				continue
			}
			sym, ok := resolveFuncSymbol(kv.Value, p, byID)
			if !ok {
				continue
			}
			reason := fmt.Sprintf("cobra %s of a command", key.Name)
			if cmdName != "" {
				reason = fmt.Sprintf("cobra %s of command %q", key.Name, cmdName)
			}
			out = append(out, EntryPoint{Symbol: sym, Kind: KindCobraCommand, Reason: reason})
		}
		return true
	})
	return out
}

// methodsByReceiver indexes every method res.Symbols reports by (package
// path, bare receiver type name), built once up front so grpcEntryPoints
// does not re-scan Symbols(res) once per candidate type.
func methodsByReceiver(res *Result) map[string]map[string][]model.CodeSymbol {
	out := map[string]map[string][]model.CodeSymbol{}
	for _, s := range Symbols(res) {
		if s.Kind != model.SymMethod {
			continue
		}
		m := out[s.Package]
		if m == nil {
			m = map[string][]model.CodeSymbol{}
			out[s.Package] = m
		}
		recv := strings.TrimPrefix(s.Receiver, "*")
		m[recv] = append(m[recv], s)
	}
	return out
}

// grpcServerInterfaces finds p's generated gRPC service interfaces: a type
// named "*Server" whose package also declares "Register*Server" — the
// protoc-gen-go-grpc pattern (RegisterGreeterServer(s *grpc.Server, srv
// GreeterServer)) that names a type as a gRPC service contract without
// flowreview depending on the grpc or protobuf packages at all.
func grpcServerInterfaces(p *packages.Package) map[string]*types.Interface {
	if p.Types == nil {
		return nil
	}
	scope := p.Types.Scope()
	var out map[string]*types.Interface
	for _, name := range scope.Names() {
		if !strings.HasSuffix(name, "Server") {
			continue
		}
		tn, ok := scope.Lookup(name).(*types.TypeName)
		if !ok {
			continue
		}
		iface, ok := tn.Type().Underlying().(*types.Interface)
		if !ok {
			continue
		}
		if scope.Lookup("Register"+name) == nil {
			continue
		}
		if out == nil {
			out = map[string]*types.Interface{}
		}
		out[name] = iface
	}
	return out
}

// grpcEntryPoints finds p's types that implement one of ifaces (p's gRPC
// service interfaces, from grpcServerInterfaces) and reports each of their
// methods that the interface declares.
func grpcEntryPoints(p *packages.Package, ifaces map[string]*types.Interface, methodsByRecv map[string][]model.CodeSymbol) []EntryPoint {
	if len(ifaces) == 0 {
		return nil
	}
	scope := p.Types.Scope()
	var out []EntryPoint
	for _, name := range scope.Names() {
		tn, ok := scope.Lookup(name).(*types.TypeName)
		if !ok || tn.IsAlias() {
			continue
		}
		named, ok := tn.Type().(*types.Named)
		if !ok {
			continue
		}
		for ifaceName, iface := range ifaces {
			if ifaceName == name || !implementsGRPCServer(named, ifaceName, iface) {
				continue
			}
			for _, m := range methodsByRecv[name] {
				if !ifaceHasMethod(iface, m.Name) {
					continue
				}
				out = append(out, EntryPoint{
					Symbol: m,
					Kind:   KindGRPCMethod,
					Reason: fmt.Sprintf("gRPC service method: implements %s (registered via Register%s)", ifaceName, ifaceName),
				})
			}
		}
	}
	return out
}

// implementsGRPCServer reports whether named is a gRPC service
// implementation of ifaceName/iface: either it genuinely satisfies the
// interface (types.Implements, checked both by value and by pointer, since
// generated service structs are conventionally used with a pointer receiver
// but grpc-go does not require one), or — the strong hint this slice asks
// for — it embeds the generated "Unimplemented<Name>" base struct, which
// satisfies the interface's forward-compatibility marker method even when
// named itself only implements a subset of the RPCs.
func implementsGRPCServer(named *types.Named, ifaceName string, iface *types.Interface) bool {
	if types.Implements(named, iface) || types.Implements(types.NewPointer(named), iface) {
		return true
	}
	st, ok := named.Underlying().(*types.Struct)
	if !ok {
		return false
	}
	want := "Unimplemented" + ifaceName
	for i := 0; i < st.NumFields(); i++ {
		f := st.Field(i)
		if f.Embedded() && embeddedTypeName(f.Type()) == want {
			return true
		}
	}
	return false
}

// embeddedTypeName is t's bare type name, modulo one pointer indirection: the
// receiver-type convention goID/structuralID use (see symbols.go), and also
// exactly what an embedded field's type name is for the grpc "strong hint"
// check above.
func embeddedTypeName(t types.Type) string {
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	named, ok := t.(*types.Named)
	if !ok {
		return ""
	}
	return named.Obj().Name()
}

func ifaceHasMethod(iface *types.Interface, name string) bool {
	for i := 0; i < iface.NumMethods(); i++ {
		if iface.Method(i).Name() == name {
			return true
		}
	}
	return false
}
