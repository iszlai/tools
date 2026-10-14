# third_party/llama

The embedded model sidecar is [llama.cpp](https://github.com/ggml-org/llama.cpp)'s
`llama-server`, pinned to release **b11609** (macOS arm64, Metal).

## What's here

| Path | Committed? | What |
|---|---|---|
| `llama.go` | yes | `Version` constant — the pin |
| `embed_on.go` / `embed_off.go` | yes | `go:embed dist` behind the `llama_embed` build tag, and the no-op fallback |
| `SHA256SUMS` | yes | checksum of the release asset `fetch.sh` downloads |
| `LICENSE` | yes | llama.cpp's own licence (MIT), copied unmodified from the release |
| `fetch.sh` | yes | downloads, verifies, unpacks |
| `*.tar.gz` | no (gitignored) | the downloaded archive |
| `dist/` | no (gitignored) | the unpacked `llama-server` + the dylibs it needs |

## Fetching the build

```sh
make fetch-llama
```

Downloads `llama-b11609-bin-macos-arm64.tar.gz` from the pinned GitHub
release, verifies it against `SHA256SUMS`, and copies just `llama-server` and
the dylibs `otool -L` shows it actually loads into `dist/` (dereferencing the
release's symlinks, since `go:embed` cannot follow them). Nothing else from
the ~12 MB archive — the CLI tools, benchmarks and RPC server it also ships —
is kept.

Re-running `make fetch-llama` is a no-op once the archive matches
`SHA256SUMS`; it only re-downloads when missing or mismatched.

## Building with it embedded

```sh
make build
```

adds `-tags llama_embed` automatically once `dist/llama-server` exists (see
the `LLAMA_TAGS` conditional in `../../Makefile`). Without it — a fresh
checkout, or `go build ./...` directly — the binary still compiles: FS is
empty and `Available()` is `false`, and callers fall back to
`internal/llm/stub`.

## Bumping the pin

Update, together: `Version` in `llama.go`, the version string in `fetch.sh`,
and `SHA256SUMS` (the checksum for the new release's
`llama-<version>-bin-macos-arm64.tar.gz`, from its GitHub release page or
`shasum -a 256` on the downloaded file). Then `make fetch-llama` and confirm
`dist/llama-server --version` reports the new build.
