package treesitter

import (
	"unsafe"

	tsyaml "github.com/tree-sitter-grammars/tree-sitter-yaml/bindings/go"
	tsgo "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tsjava "github.com/tree-sitter/tree-sitter-java/bindings/go"
	tsjs "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tspython "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tsrust "github.com/tree-sitter/tree-sitter-rust/bindings/go"
	tstypescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// grammarPlugin is everything New needs to register one grammar. Adding a
// language is: add its Go binding module to go.mod, add a query file under
// queries/, and add one entry here.
//
// Two languages from the FR-39 scope are missing on purpose, see the FR-39
// report for what was tried:
//   - Gherkin has no official or working community Go tree-sitter binding;
//     it is handled by the hand-written line parser in gherkin.go instead.
//   - SQL (github.com/DerekStride/tree-sitter-sql, the grammar nvim-treesitter
//     uses) resolves through the proxy but its published module ships
//     without the generated src/parser.c, so it fails to compile; the one
//     community fork found with parser.c committed has no Go binding.
//     Adding SQL back is: find or vendor a Go-bindable SQL grammar, write
//     queries/sql.scm, add one entry here.
type grammarPlugin struct {
	name        string // the lang.Detect() name this grammar answers for
	queryFile   string // queries/<queryFile>
	reclassify  bool   // see grammarSpec.reclassify
	languagePtr func() unsafe.Pointer
}

var grammarPlugins = []grammarPlugin{
	{name: "go", queryFile: "go.scm", languagePtr: tsgo.Language},
	{name: "java", queryFile: "java.scm", languagePtr: tsjava.Language},
	{name: "javascript", queryFile: "javascript.scm", languagePtr: tsjs.Language},
	{name: "typescript", queryFile: "typescript.scm", languagePtr: tstypescript.LanguageTypescript},
	{name: "tsx", queryFile: "typescript.scm", languagePtr: tstypescript.LanguageTSX},
	{name: "python", queryFile: "python.scm", reclassify: true, languagePtr: tspython.Language},
	{name: "rust", queryFile: "rust.scm", reclassify: true, languagePtr: tsrust.Language},
	{name: "yaml", queryFile: "yaml.scm", languagePtr: tsyaml.Language},
}
