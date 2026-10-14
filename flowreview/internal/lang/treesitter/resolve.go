package treesitter

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/iszlai/tools/flowreview/internal/model"
)

// ResolveID looks up id -- as produced by Outline/FunctionAt/Callers/Callees
// ("path#Name" or "path#Recv.Name", see analyze.go's buildSymbol) -- against
// the current worktree content of its own path, returning the matching
// CodeSymbol, or (nil, nil) when id names something Outline no longer finds
// there (a closure id, which Outline never lists, or a declaration since
// edited or removed on disk). FR-18's call-chain explorer uses this to turn
// a Callers/Callees edge's bare id -- the only thing those keep of the
// symbol they matched, see calls.go -- back into a path and line to jump to.
func (p *Provider) ResolveID(ctx context.Context, id string) (*model.CodeSymbol, error) {
	path, _, ok := strings.Cut(id, "#")
	if !ok {
		return nil, nil
	}
	src, err := os.ReadFile(filepath.Join(p.repoRoot, path))
	if err != nil {
		return nil, err
	}
	syms, err := p.Outline(ctx, path, src)
	if err != nil {
		return nil, err
	}
	for _, s := range syms {
		if s.ID == id {
			return &s, nil
		}
	}
	return nil, nil
}
