#!/usr/bin/env bash
# Install, update or remove th. Everything it touches is printed; nothing is
# overwritten silently.
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
  --prefix DIR       where the th binary goes (default: ~/.local/bin)
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
  [ -x "$prefix/th" ] || { echo ""; return; }
  "$prefix/th" version 2>/dev/null | awk '{print $2}' || echo ""
}

place() {  # place <built-binary>
  mkdir -p "$prefix"
  install -m 0755 "$1" "$prefix/th"
}

install_skill() {
  [ "$with_skill" = 1 ] || return 0
  # Only available from a checkout; a release download has no skill file.
  [ -f "$here/skill/taskhound/SKILL.md" ] || return 0
  mkdir -p "$skill_dir/taskhound"
  install -m 0644 "$here/skill/taskhound/SKILL.md" "$skill_dir/taskhound/SKILL.md"
  echo "installed $skill_dir/taskhound/SKILL.md"
}

build_from_source() {
  command -v go >/dev/null || { echo "install.sh: go is not installed" >&2; return 1; }
  # Progress goes to stderr: the caller captures stdout as the path.
  echo "building..." >&2
  (cd "$here" && go build -trimpath -o "$here/bin/th" .)
  echo "$here/bin/th"
}

fetch_release() {
  local os arch url tmp
  os=$(uname -s | tr '[:upper:]' '[:lower:]')
  arch=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')
  if [ -n "$tag" ]; then
    url="https://github.com/$REPO/releases/download/$tag/th_${os}_${arch}"
  else
    url="https://github.com/$REPO/releases/latest/download/th_${os}_${arch}"
  fi
  tmp=$(mktemp -d)/th
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
    rm -f "$prefix/th" && echo "removed $prefix/th"
    rm -f "$skill_dir/taskhound/SKILL.md" && rmdir "$skill_dir/taskhound" 2>/dev/null || true
    echo "removed $skill_dir/taskhound"
    exit 0
    ;;

  release)
    before=$(installed_version)
    binary=$(fetch_release)
    after=$("$binary" version | awk '{print $2}')
    place "$binary"
    install_skill
    if [ -n "$before" ] && [ "$before" = "$after" ]; then
      echo "already on $after — reinstalled $prefix/th"
    elif [ -n "$before" ]; then
      echo "updated $prefix/th: $before -> $after"
    else
      echo "installed $prefix/th ($after)"
    fi
    path_note
    exit 0
    ;;

  update)
    before=$(installed_version)
    if [ -f "$here/main.go" ] && command -v go >/dev/null; then
      binary=$(build_from_source)
      after=$("$binary" version | awk '{print $2}')
      place "$binary"
      install_skill
      echo "updated $prefix/th from this checkout (was ${before:-nothing}, now $after)"
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
      echo "updated $prefix/th: ${before:-nothing} -> $after"
    fi
    path_note
    exit 0
    ;;

  install)
    binary=$(build_from_source)
    place "$binary"
    echo "installed $prefix/th"
    install_skill
    path_note
    echo
    echo "next: cd into a repo and run  th init"
    ;;
esac
