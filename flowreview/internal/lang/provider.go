// Package lang defines how flowreview learns about code: a LanguageProvider
// per tier. The structural tier (tree-sitter) covers every language with a
// grammar; the exact tier (go/packages, go/ssa) covers Go.
package lang

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/iszlai/tools/flowreview/internal/model"
)

// ErrUnsupported is returned by a provider asked for something its
// Capabilities say it cannot do.
var ErrUnsupported = errors.New("lang: not supported by this provider")

// Tier says how far a provider's answers can be trusted.
type Tier string

const (
	TierStructural Tier = "structural" // syntax only; edges are heuristic
	TierExact      Tier = "exact"      // type-checked; edges are static or dispatch
)

// Capabilities says what a provider can answer.
type Capabilities struct {
	Name            string   `json:"name"`
	Tier            Tier     `json:"tier"`
	Languages       []string `json:"languages"`
	Definitions     bool     `json:"definitions"`
	Callers         bool     `json:"callers"`
	Callees         bool     `json:"callees"`
	Implementations bool     `json:"implementations"`
	EntryPoints     bool     `json:"entry_points"`
}

// LanguageProvider answers questions about code in one repository. A
// provider is constructed for a repo root; paths are repo-relative. Source is
// passed in where the revision matters (old vs new side of a diff).
type LanguageProvider interface {
	Capabilities() Capabilities
	// Supports reports whether the provider understands the file at path.
	Supports(path string) bool
	// FunctionAt returns the innermost function, method or class member
	// enclosing line in src, or nil when there is none.
	FunctionAt(ctx context.Context, path string, src []byte, line int) (*model.CodeSymbol, error)
	// Outline lists the symbols declared in src, in source order.
	Outline(ctx context.Context, path string, src []byte) ([]model.CodeSymbol, error)
	// Callers, Callees and Implementations return every candidate, each with
	// its Confidence; a provider never picks one and drops the others.
	Callers(ctx context.Context, sym model.CodeSymbol) ([]model.NavigationEdge, error)
	Callees(ctx context.Context, sym model.CodeSymbol) ([]model.NavigationEdge, error)
	Implementations(ctx context.Context, sym model.CodeSymbol) ([]model.NavigationEdge, error)
}

// Detect names the language of a path, or "" when unknown. These names are
// the vocabulary of model.ChangedFile.Language and Capabilities.Languages.
func Detect(path string) string {
	base := strings.ToLower(filepath.Base(path))
	switch base {
	case "dockerfile":
		return "dockerfile"
	case "makefile", "gnumakefile":
		return "make"
	case "go.mod", "go.sum", "go.work":
		return "gomod"
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go"
	case ".java":
		return "java"
	case ".kt", ".kts":
		return "kotlin"
	case ".js", ".mjs", ".cjs", ".jsx":
		return "javascript"
	case ".ts", ".mts", ".cts":
		return "typescript"
	case ".tsx":
		return "tsx"
	case ".feature":
		return "gherkin"
	case ".yaml", ".yml":
		return "yaml"
	case ".sql":
		return "sql"
	case ".py":
		return "python"
	case ".rs":
		return "rust"
	case ".proto":
		return "protobuf"
	case ".json":
		return "json"
	case ".md", ".markdown":
		return "markdown"
	case ".sh", ".bash", ".zsh":
		return "shell"
	case ".html", ".htm":
		return "html"
	case ".css":
		return "css"
	case ".toml":
		return "toml"
	case ".tf", ".hcl":
		return "hcl"
	}
	return ""
}
