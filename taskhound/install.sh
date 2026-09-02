#!/usr/bin/env bash
# Build th, drop it on your PATH, and install the skill that teaches agents to
# use it. Everything it touches is printed; nothing is overwritten silently.
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
prefix="${PREFIX:-$HOME/.local/bin}"
skill_dir="${CLAUDE_SKILLS:-$HOME/.claude/skills}"
with_skill=1
uninstall=0

usage() {
  cat <<'EOF'
usage: ./install.sh [--prefix DIR] [--skill-dir DIR] [--no-skill] [--uninstall]

  --prefix DIR      where to put the th binary (default: ~/.local/bin)
  --skill-dir DIR   where to put the agent skill (default: ~/.claude/skills)
  --no-skill        binary only
  --uninstall       remove both again
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --prefix)     prefix="$2"; shift 2 ;;
    --skill-dir)  skill_dir="$2"; shift 2 ;;
    --no-skill)   with_skill=0; shift ;;
    --uninstall)  uninstall=1; shift ;;
    -h|--help)    usage; exit 0 ;;
    *) echo "install.sh: unknown option $1" >&2; usage >&2; exit 2 ;;
  esac
done

if [ "$uninstall" = 1 ]; then
  rm -f "$prefix/th"           && echo "removed $prefix/th"
  rm -rf "$skill_dir/taskhound" && echo "removed $skill_dir/taskhound"
  exit 0
fi

command -v go >/dev/null || { echo "install.sh: go is not installed" >&2; exit 1; }

echo "building..."
(cd "$here" && go build -trimpath -o "$here/bin/th" .)

mkdir -p "$prefix"
install -m 0755 "$here/bin/th" "$prefix/th"
echo "installed $prefix/th"

if [ "$with_skill" = 1 ]; then
  mkdir -p "$skill_dir/taskhound"
  install -m 0644 "$here/skill/taskhound/SKILL.md" "$skill_dir/taskhound/SKILL.md"
  echo "installed $skill_dir/taskhound/SKILL.md"
fi

case ":$PATH:" in
  *":$prefix:"*) ;;
  *) echo; echo "note: $prefix is not on your PATH. Add it:"
     echo "  echo 'export PATH=\"$prefix:\$PATH\"' >> ~/.zshrc" ;;
esac

echo
echo "next: cd into a repo and run  th init"
