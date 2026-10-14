// Symbols, definitions and changed-symbol detection on top of a Load result.
// Everything here reads a *Result FR-13 already produced; nothing here
// triggers a Load itself.

package golang

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"

	"github.com/iszlai/tools/flowreview/internal/model"
)

// relPath turns an absolute file path into the repo-relative path CodeSymbol
// and model.Location carry, using pkg's module root. A package with no module
// (the standard library, or a dependency loaded from GOPATH) has nothing to
// relativize against, so abs is returned unchanged — still correct, just not
// repo-relative, which only matters for files actually inside the repo.
func relPath(pkg *packages.Package, abs string) string {
	if pkg != nil && pkg.Module != nil && pkg.Module.Dir != "" {
		if rel, err := filepath.Rel(pkg.Module.Dir, abs); err == nil {
			return filepath.ToSlash(rel)
		}
	}
	return abs
}

// findFile resolves a repo-relative path (as a CodeSymbol.Path or a
// Definition argument) back to the absolute file path and the package that
// owns it, using Result.FileToPkg as the source of truth for ownership —
// the same map Symbols uses, so a path accepted by one is accepted by the
// other.
func findFile(res *Result, path string) (abs string, pkg *packages.Package, ok bool) {
	for a, p := range res.FileToPkg {
		if relPath(p, a) == path {
			return a, p, true
		}
	}
	return "", nil, false
}

// receiverTypeName returns a method's receiver type's bare name — without a
// pointer star or type parameters, so "*Stack[T]" and "Stack[T]" both give
// "Stack" — and whether the receiver is a pointer. FR-14's ID convention (see
// goID) uses the bare name regardless of pointer-ness, so a value-receiver and
// a pointer-receiver method of the same type never collide and a caller never
// has to guess which star-ness to ask for; Receiver carries the star
// separately.
func receiverTypeName(expr ast.Expr) (name string, ptr bool) {
	if star, ok := expr.(*ast.StarExpr); ok {
		ptr = true
		expr = star.X
	}
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name, ptr
	case *ast.IndexExpr: // one type param: Stack[T]
		if id, ok := e.X.(*ast.Ident); ok {
			return id.Name, ptr
		}
	case *ast.IndexListExpr: // multiple type params: Pair[K, V]
		if id, ok := e.X.(*ast.Ident); ok {
			return id.Name, ptr
		}
	}
	return "", ptr
}

// goID builds the exact-tier ID model.go documents: "pkgpath.Name" for a
// func or type, "pkgpath.(Recv).Name" for a method — Recv is always the bare
// receiver name, never "*Recv", so the ID is stable across a receiver
// changing from value to pointer or back.
func goID(pkgPath, recv, name string) string {
	if recv == "" {
		return pkgPath + "." + name
	}
	return fmt.Sprintf("%s.(%s).%s", pkgPath, strings.TrimPrefix(recv, "*"), name)
}

// structuralID builds the structural-tier ID model.go documents ("path#Name",
// "path#Class.Name"), used wherever symbols.go has only syntax and no
// resolvable package path: Provider.FunctionAt/Outline (syntax only, by
// design — see Provider's doc comment) and a symbol in a file whose package
// no longer exists on the new side (a whole file deletion — see
// ChangedSymbolsDetailed).
func structuralID(path, recv, name string) string {
	if recv == "" {
		return path + "#" + name
	}
	return fmt.Sprintf("%s#%s.%s", path, strings.TrimPrefix(recv, "*"), name)
}

// funcTypeText renders a function's receiver and type (type params, params,
// results) exactly as written, ignoring the body: go/printer re-renders pure
// syntax, which normalizes irrelevant whitespace differences without
// resolving any type. It is used to compare a declaration's shape across two
// revisions without a type checker (the old revision may not even build), so
// it is syntactic, not semantic: a parameter whose spelled-out type name is
// unchanged but now refers to a different, shadowing import would not be
// detected as a change. See ChangedSymbolsDetailed.
func funcTypeText(fset *token.FileSet, recv *ast.FieldList, typ *ast.FuncType) string {
	var buf strings.Builder
	cfg := printer.Config{Mode: printer.UseSpaces, Tabwidth: 8}
	if recv != nil {
		cfg.Fprint(&buf, fset, recv)
		buf.WriteByte(' ')
	}
	cfg.Fprint(&buf, fset, typ)
	return buf.String()
}

// declInfo is one top-level declaration — a func, method, type or interface —
// found by parsing a file with or without type information. symbolsForPackage
// builds these from a type-checked *ast.File; ChangedSymbolsDetailed's old
// side builds the same shape from a go/parser-only parse of the pre-diff
// content, so both sides compare like for like.
type declInfo struct {
	nameIdent  *ast.Ident
	name       string
	kind       model.SymbolKind
	recv       string // "" or "Name"/"*Name"
	start, end token.Pos
	fnRecv     *ast.FieldList // non-nil for a func/method
	fnType     *ast.FuncType  // non-nil for a func/method
	typeExpr   ast.Expr       // non-nil for a type/interface
}

func (d declInfo) id(qualifier string, structural bool) string {
	if structural {
		return structuralID(qualifier, d.recv, d.name)
	}
	return goID(qualifier, d.recv, d.name)
}

// sigText is d's syntactic signature: the function type for a func/method,
// the type expression for a type/interface. Always syntax, never resolved —
// see funcTypeText.
func (d declInfo) sigText(fset *token.FileSet) string {
	if d.fnType != nil {
		return funcTypeText(fset, d.fnRecv, d.fnType)
	}
	var buf strings.Builder
	printer.Fprint(&buf, fset, d.typeExpr)
	return buf.String()
}

// declsFromFile lists file's top-level func, method, type and interface
// declarations in source order. It only needs file's AST, so it works
// identically on a type-checked *ast.File (symbolsForPackage) and on a bare
// go/parser.ParseFile result (Provider.FunctionAt/Outline, and the old side
// of ChangedSymbolsDetailed).
func declsFromFile(file *ast.File) []declInfo {
	var out []declInfo
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			kind := model.SymFunc
			recv := ""
			if d.Recv != nil && len(d.Recv.List) > 0 {
				kind = model.SymMethod
				rn, ptr := receiverTypeName(d.Recv.List[0].Type)
				recv = rn
				if ptr {
					recv = "*" + rn
				}
			}
			out = append(out, declInfo{
				nameIdent: d.Name, name: d.Name.Name, kind: kind, recv: recv,
				start: d.Pos(), end: d.End(), fnRecv: d.Recv, fnType: d.Type,
			})
		case *ast.GenDecl:
			if d.Tok != token.TYPE {
				continue
			}
			for _, spec := range d.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				kind := model.SymType
				if _, ok := ts.Type.(*ast.InterfaceType); ok {
					kind = model.SymInterface
				}
				out = append(out, declInfo{
					nameIdent: ts.Name, name: ts.Name.Name, kind: kind,
					start: ts.Pos(), end: ts.End(), typeExpr: ts.Type,
				})
			}
		}
	}
	return out
}

// defObject is the types.Object a type-checked package's info recorded for
// d's name, or nil when p has no type info (never true for a package in
// res.Packages/res.ByPath; always true when declsFromFile ran on a bare
// go/parser parse).
func defObject(p *packages.Package, d declInfo) types.Object {
	if p.TypesInfo == nil || d.nameIdent == nil {
		return nil
	}
	return p.TypesInfo.Defs[d.nameIdent]
}

// symbolsForPackage builds every CodeSymbol p declares. A file is skipped
// unless res.FileToPkg says p is the file's canonical owner: with Tests
// enabled, go/packages.Load can hand back more than one package variant for
// the same file (the plain package and its "[pkg.test]" augmented variant),
// each with its own *ast.File for that file; without this guard, a shared
// file's symbols would be built twice. FileToPkg's choice of owner is
// arbitrary between variants but consistent — the same map Symbols,
// SymbolAt, Definition and findFile all key off.
func symbolsForPackage(res *Result, p *packages.Package) []model.CodeSymbol {
	var out []model.CodeSymbol
	for _, file := range p.Syntax {
		filename := res.Fset.Position(file.Pos()).Filename
		if res.FileToPkg[filename] != p {
			continue
		}
		repoPath := relPath(p, filename)
		for _, d := range declsFromFile(file) {
			start := res.Fset.Position(d.start)
			end := res.Fset.Position(d.end)
			sig := ""
			if obj := defObject(p, d); obj != nil {
				t := obj.Type()
				if d.kind == model.SymType || d.kind == model.SymInterface {
					// Print the definition (struct fields / interface method
					// set), not just the name types.TypeString would print for
					// a lone *types.Named. For an interface this still lists
					// an embedded interface by its own name, matching
					// go/types' own formatting (and `go doc`'s) — it is not
					// flattened into the embedded interface's methods.
					t = t.Underlying()
				}
				sig = types.TypeString(t, types.RelativeTo(p.Types))
			}
			out = append(out, model.CodeSymbol{
				ID:        d.id(p.PkgPath, false),
				Name:      d.name,
				Kind:      d.kind,
				Language:  "go",
				Package:   p.PkgPath,
				Receiver:  d.recv,
				Path:      repoPath,
				Range:     model.Range{StartLine: start.Line, EndLine: end.Line},
				Signature: sig,
				Exported:  ast.IsExported(d.name),
			})
		}
	}
	return out
}

// Symbols lists every func, method, type and interface declared in res's
// loaded packages, sorted by path then start line.
func Symbols(res *Result) []model.CodeSymbol {
	if res == nil {
		return nil
	}
	seen := map[*packages.Package]bool{}
	var out []model.CodeSymbol
	for _, p := range res.FileToPkg {
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, symbolsForPackage(res, p)...)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Range.StartLine < out[j].Range.StartLine
	})
	return out
}

// SymbolAt returns the innermost of res's symbols enclosing line in path, or
// nil when none does. Go's top-level declarations never nest syntactically,
// so "innermost" only matters when ranges merely overlap (never observed in
// practice); the narrowest range wins so a tie always resolves the same way.
func SymbolAt(res *Result, path string, line int) *model.CodeSymbol {
	var best *model.CodeSymbol
	for _, s := range Symbols(res) {
		if s.Path != path || line < s.Range.StartLine || line > s.Range.EndLine {
			continue
		}
		if best == nil || (s.Range.EndLine-s.Range.StartLine) < (best.Range.EndLine-best.Range.StartLine) {
			sym := s
			best = &sym
		}
	}
	return best
}

// identAt finds the *ast.Ident at (path, line, col) — a 1-based line and a
// 1-based byte column within that line, matching model.Location — and the
// package that owns path.
func identAt(res *Result, path string, line, col int) (*packages.Package, *ast.Ident, error) {
	abs, pkg, ok := findFile(res, path)
	if !ok {
		return nil, nil, fmt.Errorf("golang: %q is not in the loaded scope", path)
	}
	var file *ast.File
	for _, f := range pkg.Syntax {
		if res.Fset.Position(f.Pos()).Filename == abs {
			file = f
			break
		}
	}
	if file == nil {
		return nil, nil, fmt.Errorf("golang: %q has no syntax tree", path)
	}

	var found *ast.Ident
	ast.Inspect(file, func(n ast.Node) bool {
		if found != nil {
			return false
		}
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		start := res.Fset.Position(id.Pos())
		end := res.Fset.Position(id.End())
		if start.Line != line || col < start.Column || col >= end.Column {
			return true
		}
		found = id
		return false
	})
	if found == nil {
		return nil, nil, fmt.Errorf("golang: no identifier at %s:%d:%d", path, line, col)
	}
	return pkg, found, nil
}

// objectPackage is the *packages.Package obj was declared in, found via
// Result.ByPath — which, unlike Packages, includes every package reachable
// from the loaded scope (dependencies included), so this resolves an object
// declared outside the scope too, as long as it is reachable from it.
func objectPackage(res *Result, obj types.Object) *packages.Package {
	if obj.Pkg() == nil {
		return nil
	}
	return res.ByPath[obj.Pkg().Path()]
}

// Definition resolves the identifier at (path, line, col) to the source
// location of its declaration, via go/types' Uses/Defs maps built during
// Load. It works across every package in the load set — an identifier
// naming an object declared in a different loaded package (plainly imported
// or dot-imported; go/types records a dot-imported name's Uses exactly like
// a qualified one) resolves correctly because that package was type-checked
// too. For an object declared outside the load set, Result.ByPath still
// finds its package when the package is reachable (e.g. a dependency, not a
// changed-or-reverse-importer package); if the object's declaration position
// is not valid even then (no source was parsed for it — a binary-only
// package, or a predeclared/builtin identifier), Definition returns
// (nil, nil): the identifier resolved to something real, there is simply
// nowhere to navigate.
func Definition(res *Result, path string, line, col int) (*model.Location, error) {
	pkg, ident, err := identAt(res, path, line, col)
	if err != nil {
		return nil, err
	}
	if pkg.TypesInfo == nil {
		return nil, fmt.Errorf("golang: package %s has no type info", pkg.PkgPath)
	}
	obj := pkg.TypesInfo.Uses[ident]
	if obj == nil {
		obj = pkg.TypesInfo.Defs[ident]
	}
	if obj == nil {
		return nil, fmt.Errorf("golang: %s:%d:%d did not resolve to an object", path, line, col)
	}
	if !obj.Pos().IsValid() {
		return nil, nil
	}
	p := res.Fset.Position(obj.Pos())
	return &model.Location{
		Path: relPath(objectPackage(res, obj), p.Filename),
		Line: p.Line,
		Col:  p.Column,
	}, nil
}

// hunkNewRange is the new-side line range a hunk covers: [NewStart,
// NewStart+NewLines-1], or the single point NewStart when NewLines is 0 (a
// pure-deletion hunk adds no new lines; NewStart is still the new-side line
// the deletion sits next to, so a decl whose range includes that boundary
// line is still treated as touched — this is the "context-adjacent
// deletions" case).
func hunkNewRange(h model.DiffHunk) (start, end int) {
	if h.NewLines == 0 {
		return h.NewStart, h.NewStart
	}
	return h.NewStart, h.NewStart + h.NewLines - 1
}

// hunkOldRange is hunkNewRange's mirror on the old side, used to find which
// old-side declarations a hunk touches.
func hunkOldRange(h model.DiffHunk) (start, end int) {
	if h.OldLines == 0 {
		return h.OldStart, h.OldStart
	}
	return h.OldStart, h.OldStart + h.OldLines - 1
}

func rangesOverlap(aStart, aEnd, bStart, bEnd int) bool {
	return aStart <= bEnd && bStart <= aEnd
}

// ChangedSymbols is res's symbols whose declaration range intersects a hunk
// of files, marked Changed. A deleted file contributes nothing (there is no
// new-side declaration to mark); see ChangedSymbolsDetailed for deletions.
func ChangedSymbols(res *Result, files []model.ChangedFile) []model.CodeSymbol {
	byPath := map[string][][2]int{}
	for _, f := range files {
		if f.Status == model.FileDeleted {
			continue
		}
		for _, h := range f.Hunks {
			s, e := hunkNewRange(h)
			byPath[f.Path()] = append(byPath[f.Path()], [2]int{s, e})
		}
	}

	var out []model.CodeSymbol
	for _, sym := range Symbols(res) {
		for _, r := range byPath[sym.Path] {
			if rangesOverlap(sym.Range.StartLine, sym.Range.EndLine, r[0], r[1]) {
				sym.Changed = true
				out = append(out, sym)
				break
			}
		}
	}
	return out
}

// ChangeKind classifies one Change.
type ChangeKind string

const (
	ChangeAdded    ChangeKind = "added"
	ChangeModified ChangeKind = "modified"
	ChangeDeleted  ChangeKind = "deleted"
)

// Change is one symbol touched by a diff. model.CodeSymbol has no "deleted"
// state and no before/after signature pair, so ChangedSymbolsDetailed returns
// this wrapper instead of adding either to the frozen type.
type Change struct {
	Symbol model.CodeSymbol // the new-side symbol; for Kind == ChangeDeleted this is reconstructed from old-side syntax only (Path is the old path)
	Kind   ChangeKind

	// OldSignature and NewSignature are both the syntactic form (funcTypeText
	// / declInfo.sigText), not the semantic Symbol.Signature: comparing a
	// syntax-only old side against a type-checked new side's resolved
	// signature would report a change on every symbol, formatting
	// differences alone. Empty on the side that does not apply (OldSignature
	// for ChangeAdded, NewSignature for ChangeDeleted).
	OldSignature     string
	NewSignature     string
	SignatureChanged bool
}

// OldContent reads a file's content as of the diff's old revision (the PR
// base, HEAD before a worktree edit, …). ChangedSymbolsDetailed calls it only
// for files with an old side (Modified, Renamed, Deleted) and only to find
// what the new side no longer has: without it, a deleted function cannot be
// reported, because res — the new side — has nothing left to describe it.
type OldContent func(path string) ([]byte, error)

// newSignatureText finds sym's declaration in res by ID and renders it with
// the same syntactic method as the old side, for a fair comparison in Change.
func newSignatureText(res *Result, sym model.CodeSymbol) string {
	abs, pkg, ok := findFile(res, sym.Path)
	if !ok {
		return ""
	}
	for _, file := range pkg.Syntax {
		if res.Fset.Position(file.Pos()).Filename != abs {
			continue
		}
		for _, d := range declsFromFile(file) {
			if d.id(pkg.PkgPath, false) == sym.ID {
				return d.sigText(res.Fset)
			}
		}
	}
	return ""
}

// ChangedSymbolsDetailed is ChangedSymbols with added/modified/deleted
// classification and a syntactic signature-change flag. The new side comes
// from res (type-checked); the old side is parsed with go/parser only — no
// type information, because the pre-diff revision is not necessarily part of
// the loaded scope and may not even build.
//
// Matching across the two sides is by the stable Go ID (goID/structuralID): a
// declaration is "modified" when its ID still resolves to a symbol in res and
// a hunk touched its old-side range; "deleted" when no such symbol exists;
// "added" when ChangedSymbols found it on the new side but no old-side
// declaration shared its ID. A function renamed across the diff is therefore
// reported as one deletion plus one addition — nothing pairs them, because
// nothing in a text diff says to.
//
// For a file whose package still exists on the new side (Modified, Renamed),
// deleted declarations get a real "pkgpath.Name"-style ID, matching what that
// symbol's ID would be if it still existed — so a rename that keeps the
// function but changes nothing else never happens to look like a
// delete-and-recreate of the same ID. For a wholly deleted file (Deleted),
// there is no new-side package to anchor to, so its declarations get the
// structural-tier "path#Name" ID instead (see structuralID) — honestly
// approximate rather than falsely precise.
func ChangedSymbolsDetailed(res *Result, files []model.ChangedFile, old OldContent) ([]Change, error) {
	newChanged := ChangedSymbols(res, files)
	allNewByID := make(map[string]model.CodeSymbol, len(newChanged))
	for _, s := range Symbols(res) {
		allNewByID[s.ID] = s
	}

	matched := make(map[string]bool, len(newChanged))
	var out []Change

	for _, f := range files {
		if f.Status != model.FileModified && f.Status != model.FileRenamed && f.Status != model.FileDeleted {
			continue
		}
		if !strings.HasSuffix(f.OldPath, ".go") {
			continue
		}
		src, err := old(f.OldPath)
		if err != nil {
			return nil, fmt.Errorf("golang: read old content of %q: %w", f.OldPath, err)
		}
		fset := token.NewFileSet()
		oldFile, err := parser.ParseFile(fset, f.OldPath, src, 0)
		if err != nil {
			return nil, fmt.Errorf("golang: parse old content of %q: %w", f.OldPath, err)
		}
		pkgPath, havePkg := pkgPathForNewFile(res, f)

		for _, d := range declsFromFile(oldFile) {
			r := model.Range{StartLine: fset.Position(d.start).Line, EndLine: fset.Position(d.end).Line}
			if !hunkTouchesOld(f.Hunks, r.StartLine, r.EndLine) {
				continue
			}
			oldSig := d.sigText(fset)

			var id string
			if havePkg {
				id = d.id(pkgPath, false)
			} else {
				id = d.id(f.OldPath, true)
			}

			if newSym, ok := allNewByID[id]; ok {
				matched[id] = true
				newSig := newSignatureText(res, newSym)
				out = append(out, Change{
					Symbol:           newSym,
					Kind:             ChangeModified,
					OldSignature:     oldSig,
					NewSignature:     newSig,
					SignatureChanged: newSig != "" && oldSig != newSig,
				})
				continue
			}

			out = append(out, Change{
				Symbol:       declSymbol(fset, f.OldPath, id, d),
				Kind:         ChangeDeleted,
				OldSignature: oldSig,
			})
		}
	}

	for _, s := range newChanged {
		if matched[s.ID] {
			continue
		}
		out = append(out, Change{Symbol: s, Kind: ChangeAdded, NewSignature: newSignatureText(res, s)})
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Symbol.Path != out[j].Symbol.Path {
			return out[i].Symbol.Path < out[j].Symbol.Path
		}
		return out[i].Symbol.Range.StartLine < out[j].Symbol.Range.StartLine
	})
	return out, nil
}

// pkgPathForNewFile is the import path of the package that owns f's new path
// in res, when f still has one (Modified, Renamed — not Deleted).
func pkgPathForNewFile(res *Result, f model.ChangedFile) (string, bool) {
	if f.Status == model.FileDeleted {
		return "", false
	}
	if _, pkg, ok := findFile(res, f.Path()); ok {
		return pkg.PkgPath, true
	}
	return "", false
}

// hunkTouchesOld reports whether any hunk's old-side range overlaps
// [startLine, endLine].
func hunkTouchesOld(hunks []model.DiffHunk, startLine, endLine int) bool {
	for _, h := range hunks {
		s, e := hunkOldRange(h)
		if rangesOverlap(startLine, endLine, s, e) {
			return true
		}
	}
	return false
}
