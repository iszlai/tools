#!/usr/bin/env bash
# Downloads the pinned llama.cpp macOS arm64 build, verifies it against the
# committed checksum, and unpacks only llama-server plus the dylibs it needs
# into dist/ (gitignored, not committed). Run via `make fetch-llama`.
#
# Bumping the pin: update "version" below, llama.go's Version constant and
# SHA256SUMS together.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
version="b11609"
asset="llama-${version}-bin-macos-arm64.tar.gz"
url="https://github.com/ggml-org/llama.cpp/releases/download/${version}/${asset}"
archive="$here/$asset"
dist="$here/dist"

# Exactly what llama-server loads, per `otool -L` on the pinned build: itself,
# plus the transitive closure of its @rpath dylibs. Everything else in the
# ~12 MB release (llama-cli, llama-bench, the RPC server, ...) is discarded.
files=(
  llama-server
  libllama-server-impl.dylib
  libllama-common.0.dylib
  libmtmd.0.dylib
  libllama.0.dylib
  libggml.0.dylib
  libggml-cpu.0.dylib
  libggml-blas.0.dylib
  libggml-metal.0.dylib
  libggml-rpc.0.dylib
  libggml-base.0.dylib
)

checksum() { shasum -a 256 "$1" | awk '{print $1}'; }

expected=$(awk -v f="$asset" '$2==f {print $1}' "$here/SHA256SUMS")
if [ -z "$expected" ]; then
  echo "fetch-llama: no checksum for $asset in SHA256SUMS" >&2
  exit 1
fi

if [ ! -f "$archive" ] || [ "$(checksum "$archive")" != "$expected" ]; then
  echo "fetch-llama: downloading $asset..." >&2
  curl -fL --progress-bar -o "$archive.tmp" "$url"
  mv "$archive.tmp" "$archive"
fi

got=$(checksum "$archive")
if [ "$got" != "$expected" ]; then
  echo "fetch-llama: checksum mismatch for $asset" >&2
  echo "  want $expected" >&2
  echo "  got  $got" >&2
  rm -f "$archive"
  exit 1
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
tar -xzf "$archive" -C "$work"
srcdir="$work/llama-${version}"

tmpdist="$dist.tmp.$$"
rm -rf "$tmpdist"
mkdir -p "$tmpdist"
for f in "${files[@]}"; do
  # -L dereferences: the release ships these as symlinks to the real,
  # per-build-numbered file, and go:embed cannot embed a symlink.
  cp -L "$srcdir/$f" "$tmpdist/$f"
  chmod 0755 "$tmpdist/$f"
done

rm -rf "$dist"
mv "$tmpdist" "$dist"

echo "fetch-llama: extracted ${#files[@]} files into $dist"
"$dist/llama-server" --version
