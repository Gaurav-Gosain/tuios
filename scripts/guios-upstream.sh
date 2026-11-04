#!/bin/sh
# Lists kero's changes since the commit guios took its app from
# (cmd/guios/UPSTREAM), so its later fixes can be brought over by hand.
#
#   scripts/guios-upstream.sh [kero checkout]
#
# Without a checkout it clones kero into a temporary folder. It prints the
# commits, then the files they touched that guios also has.
set -e
ROOT=$(cd "$(dirname "$0")/.." && pwd)
BASE=$(awk '/^commit /{print $2}' "$ROOT/cmd/guios/UPSTREAM")
DIR=$1
if [ -z "$DIR" ]; then
	DIR=$(mktemp -d)
	trap 'rm -rf "$DIR"' EXIT
	git clone -q https://github.com/egoist/kero.git "$DIR"
else
	git -C "$DIR" fetch -q origin
fi
HEAD=$(git -C "$DIR" rev-parse origin/HEAD 2>/dev/null || git -C "$DIR" rev-parse HEAD)
echo "kero $BASE..$HEAD"
git -C "$DIR" log --oneline --no-merges "$BASE..$HEAD"
echo
echo "Files guios has that changed upstream:"
git -C "$DIR" diff --name-only "$BASE" "$HEAD" | while read -r f; do
	case "$f" in
	internal/daemon/* | internal/cli/* | internal/service/* | internal/detach/* | cmd/*) ;;
	internal/*) [ -e "$ROOT/internal/guios/${f#internal/}" ] && echo "  $f -> internal/guios/${f#internal/}" ;;
	*.go) [ -e "$ROOT/cmd/guios/$f" ] && echo "  $f -> cmd/guios/$f" ;;
	locales/* | icons/*) echo "  $f -> cmd/guios/$f" ;;
	esac
done
