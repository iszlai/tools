#!/usr/bin/env bash
# Install, update or remove flowreview. Everything it touches is printed;
# nothing is overwritten silently.
set -euo pipefail

REPO="iszlai/tools"
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
prefix="${PREFIX:-$HOME/.local/bin}"
skill_dir="${CLAUDE_SKILLS:-$HOME/.claude/skills}"
with_skill=1
mode=install
tag=""

usage() {
  cat <<'EOF'
usage: ./install.sh [mode] [options]

modes
  (default)          build from this checkout and install
  --update           update an existing install: rebuild if this is a checkout
                     and Go is present, otherwise fetch the latest release
  --from-release [TAG]
                     download a prebuilt binary instead of building. Needs no
                     Go and no checkout. TAG defaults to the latest release.
  --uninstall        remove the binary and the skill

options
  --prefix DIR       where the flowreview binary goes (default: ~/.local/bin)
  --skill-dir DIR    where the agent skill goes (default: ~/.claude/skills)
  --no-skill         binary only
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --update)       mode=update; shift ;;
    --from-release) mode=release; shift
                    case "${1:-}" in -*|'') ;; *) tag="$1"; shift ;; esac ;;
    --uninstall)    mode=uninstall; shift ;;
    --prefix)       prefix="$2"; shift 2 ;;
    --skill-dir)    skill_dir="$2"; shift 2 ;;
    --no-skill)     with_skill=0; shift ;;
    -h|--help)      usage; exit 0 ;;
    *) echo "install.sh: unknown option $1" >&2; usage >&2; exit 2 ;;
  esac
done

# Version of whatever is already on PATH, empty if nothing is.
installed_version() {
  [ -x "$prefix/flowreview" ] || { echo ""; return; }
  "$prefix/flowreview" version 2>/dev/null | awk '{print $2}' || echo ""
}

place() {  # place <built-binary>
  mkdir -p "$prefix"
  install -m 0755 "$1" "$prefix/flowreview"
}

install_skill() {
  [ "$with_skill" = 1 ] || return 0
  # Only available from a checkout; a release download has no skill file.
  # Also tolerant of a checkout that predates FR-24, which is what adds it.
  [ -f "$here/skill/flowreview/SKILL.md" ] || return 0
  mkdir -p "$skill_dir/flowreview"
  install -m 0644 "$here/skill/flowreview/SKILL.md" "$skill_dir/flowreview/SKILL.md"
  echo "installed $skill_dir/flowreview/SKILL.md"
}

# What a build from this checkout calls itself. Same rule as the Makefile: the
# release this checkout is standing on and how far past it we are, so an update
# from source reports a version you can compare against a release instead of
# saying "dev" whatever it is.
source_version() {
  local v
  v=$(cd "$here" && git describe --tags --match 'flowreview-v*' --dirty 2>/dev/null) || v=""
  v="${v#flowreview-}"
  echo "${v:-dev}"
}

build_from_source() {
  command -v go >/dev/null || { echo "install.sh: go is not installed" >&2; return 1; }
  # Progress goes to stderr: the caller captures stdout as the path. Delegates
  # to the Makefile's build target so CGO_ENABLED and the llama_embed build
  # tag (set only once `make fetch-llama` has populated third_party/llama/dist/)
  # stay in one place.
  echo "building..." >&2
  (cd "$here" && make build VERSION="$(source_version)" 1>&2)
  echo "$here/bin/flowreview"
}

fetch_release() {
  local os arch url tmp
  os=$(uname -s | tr '[:upper:]' '[:lower:]')
  arch=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
  if [ -n "$tag" ]; then
    url="https://github.com/$REPO/releases/download/$tag/flowreview_${os}_${arch}"
  else
    url="https://github.com/$REPO/releases/latest/download/flowreview_${os}_${arch}"
  fi
  tmp=$(mktemp -d)/flowreview
  echo "downloading ${tag:-latest} for ${os}/${arch}..." >&2
  curl -fsSL "$url" -o "$tmp" || {
    echo "install.sh: no build published at $url" >&2
    return 1
  }
  chmod +x "$tmp"
  echo "$tmp"
}

path_note() {
  case ":$PATH:" in
    *":$prefix:"*) ;;
    *) echo; echo "note: $prefix is not on your PATH. Add it:"
       echo "  echo 'export PATH=\"$prefix:\$PATH\"' >> ~/.zshrc" ;;
  esac
}

case "$mode" in
  uninstall)
    rm -f "$prefix/flowreview" && echo "removed $prefix/flowreview"
    rm -f "$skill_dir/flowreview/SKILL.md" && rmdir "$skill_dir/flowreview" 2>/dev/null || true
    echo "removed $skill_dir/flowreview"
    exit 0
    ;;

  release)
    before=$(installed_version)
    binary=$(fetch_release)
    after=$("$binary" version | awk '{print $2}')
    place "$binary"
    install_skill
    if [ -n "$before" ] && [ "$before" = "$after" ]; then
      echo "already on $after — reinstalled $prefix/flowreview"
    elif [ -n "$before" ]; then
      echo "updated $prefix/flowreview: $before -> $after"
    else
      echo "installed $prefix/flowreview ($after)"
    fi
    path_note
    exit 0
    ;;

  update)
    before=$(installed_version)
    if [ -f "$here/cmd/flowreview/main.go" ] && command -v go >/dev/null; then
      binary=$(build_from_source)
      after=$("$binary" version | awk '{print $2}')
      place "$binary"
      install_skill
      echo "updated $prefix/flowreview from this checkout (was ${before:-nothing}, now $after)"
    else
      # Not a checkout, or no Go: take the latest published build instead.
      binary=$(fetch_release)
      after=$("$binary" version | awk '{print $2}')
      if [ -n "$before" ] && [ "$before" = "$after" ]; then
        echo "already on the latest release ($after) — nothing to do"
        exit 0
      fi
      place "$binary"
      install_skill
      echo "updated $prefix/flowreview: ${before:-nothing} -> $after"
    fi
    path_note
    exit 0
    ;;

  install)
    binary=$(build_from_source)
    place "$binary"
    echo "installed $prefix/flowreview"
    install_skill
    path_note
    echo
    echo "next: cd into a repo and run  flowreview"
    ;;
esac
