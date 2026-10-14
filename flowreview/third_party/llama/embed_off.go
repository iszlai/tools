//go:build !llama_embed

package llama

import "io/fs"

// FS is empty: this binary has no embedded llama-server build. Build with
// -tags llama_embed (the Makefile does this automatically once `make
// fetch-llama` has populated dist/) to embed one.
var FS fs.FS = noFS{}

// Available reports whether a real llama-server build is embedded in this
// binary.
func Available() bool { return false }

// noFS is an fs.FS with nothing in it.
type noFS struct{}

func (noFS) Open(name string) (fs.File, error) {
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}
