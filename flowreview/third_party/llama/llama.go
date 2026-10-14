// Package llama holds the pinned llama.cpp build that flowreview's sidecar
// embeds. The server binary and its dylibs are not committed: `make
// fetch-llama` downloads the release asset named below, verifies it against
// SHA256SUMS, and unpacks only what llama-server needs into dist/, which is
// gitignored. LICENSE carries llama.cpp's own licence (MIT) alongside the
// binary it covers, as the release ships it.
//
// FS (see embed_on.go / embed_off.go) is only populated when this binary was
// built with -tags llama_embed; the Makefile adds that tag automatically once
// dist/ exists.
package llama

// Version is the pinned llama.cpp release tag (github.com/ggml-org/llama.cpp
// — its CI tags every build "b<number>" rather than cutting traditional
// versioned releases). Bumping the pin means updating this constant,
// fetch.sh and SHA256SUMS together.
const Version = "b11609"
