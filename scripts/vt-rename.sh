#!/usr/bin/env bash
# Moves a branch made before internal/vt became the module tuios.dev/vt (in
# the folder vt/) across the rename. Run it after rebasing onto a main that
# has the rename:
#
#   git rebase origin/main      # git's rename detection carries most hunks
#   scripts/vt-rename.sh        # fixes what the rebase could not
#
# It moves any file the branch added under internal/vt/ into vt/, rewrites
# the import paths in every .go file, rewrites internal/vt paths in the docs,
# the scripts and the justfile, and runs gofmt. The workflows are left alone:
# fuzz.yml keeps internal/vt as the name of its corpus caches on purpose. Run
# it again at any time: a tree with nothing left to change stays as it is.
set -euo pipefail

cd "$(dirname "$0")/.."

# Files the branch added under the old folder.
if [ -d internal/vt ]; then
	while IFS= read -r -d '' f; do
		dest="vt/${f#internal/vt/}"
		mkdir -p "$(dirname "$dest")"
		git mv -k "$f" "$dest"
		[ -e "$f" ] && mv "$f" "$dest"
		echo "moved $f -> $dest"
	done < <(find internal/vt -type f -print0)
	find internal/vt -type d -empty -delete
fi

# Import paths. The vtgen path first: it was internal/fuzz/vtgen once.
git ls-files -z -- '*.go' |
	xargs -0 -r grep -lZ -e 'github.com/Gaurav-Gosain/tuios/internal/vt' -e 'github.com/Gaurav-Gosain/tuios/internal/fuzz/vtgen' |
	xargs -0 -r sed -i \
		-e 's#github.com/Gaurav-Gosain/tuios/internal/fuzz/vtgen#tuios.dev/vt/vtgen#g' \
		-e 's#github.com/Gaurav-Gosain/tuios/internal/vt\([/"]\)#tuios.dev/vt\1#g'

# Paths in the docs, the scripts and the justfile.
git ls-files -z -- '*.md' 'scripts/*.sh' 'justfile' |
	xargs -0 -r grep -lZ 'internal/vt' |
	{ grep -zv '^scripts/vt-rename.sh$' || true; } |
	xargs -0 -r sed -i -e 's#\binternal/vt\b#vt#g'

git ls-files -z -- '*.go' | xargs -0 -r grep -lZ 'tuios.dev/vt' | xargs -0 -r gofmt -w
echo "done. Check with: go build ./... && go vet ./... ./vt/..."
