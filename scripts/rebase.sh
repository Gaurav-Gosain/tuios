#!/usr/bin/env bash
# Rebases the current branch with `git rebase "$@"` and regenerates a packed
# archive at every commit where it conflicts.
#
#   scripts/rebase.sh origin/main
#
# Some packages embed a gzip archive that `go generate` packs from the files
# beside it (AGENTS.md, Generated files). Two pull requests that edit those
# files always conflict on the archive, and git cannot merge a gzip file. The
# archive holds nothing the source files do not, so the fix is to run the
# generator again on the merged files. This script does that at each stop
# where every conflicted path is one of the archives below, and continues. A
# stop with any other conflicted path is a real conflict: the script leaves the
# rebase there and exits 1 for a person to resolve. After that, run
# `scripts/rebase.sh --continue`.
#
# Run it from inside the repository. Arguments go to `git rebase` as they are.
set -euo pipefail

# generator prints the package whose go:generate packs the archive at path $1,
# and nothing for any other path. A case, not an array: macOS ships bash 3.2.
generator() {
	case $1 in
	skills/tuios.tar.gz) echo ./skills ;;
	internal/session/verbdocs.tar.gz) echo ./internal/session ;;
	internal/learn/lessons/lessons.tar.gz) echo ./internal/learn/lessons ;;
	internal/integration/assets.tar.gz) echo ./internal/integration ;;
	harness/manifests.tar.gz) echo ./harness ;;
	esac
}

cd "$(git rev-parse --show-toplevel)"

in_rebase() {
	local d
	d=$(git rev-parse --git-path rebase-merge)
	[ -d "$d" ] || [ -d "$(git rev-parse --git-path rebase-apply)" ]
}

step() {
	GIT_EDITOR=true git rebase "$@"
}

if step "$@"; then
	exit 0
fi
while in_rebase; do
	conflicted=$(git diff --name-only --diff-filter=U)
	only_archives=${conflicted:+yes}
	for path in $conflicted; do
		[ -n "$(generator "$path")" ] || only_archives=
	done
	if [ -z "$only_archives" ]; then
		echo "rebase.sh: the rebase stopped. Conflicted paths:" >&2
		printf '%s\n' "${conflicted:-(none)}" >&2
		echo "rebase.sh: resolve them, then run scripts/rebase.sh --continue" >&2
		exit 1
	fi
	for path in $conflicted; do
		pkg=$(generator "$path")
		go generate "$pkg"
		git add -- "$path" "${pkg#./}"
		echo "rebase.sh: regenerated $path" >&2
	done
	if step --continue; then
		exit 0
	fi
done
# git rebase failed without a rebase in progress, such as on a bad argument.
exit 1
