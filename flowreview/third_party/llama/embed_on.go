//go:build llama_embed

package llama

import (
	"embed"
	"io/fs"
)

//go:embed dist
var distFS embed.FS

// FS holds the embedded llama-server build: llama-server and its dylibs are
// its direct entries (the "dist/" prefix go:embed adds is stripped away).
var FS fs.FS = sub(distFS)

// Available reports whether a real llama-server build is embedded in this
// binary.
func Available() bool { return true }

func sub(f embed.FS) fs.FS {
	s, err := fs.Sub(f, "dist")
	if err != nil {
		// Only reachable if dist/ was empty at build time, i.e. -tags
		// llama_embed was used without first running `make fetch-llama`.
		panic("third_party/llama: " + err.Error())
	}
	return s
}
