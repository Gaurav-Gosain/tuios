#!/usr/bin/env bash
# Rebases the current branch with `git rebase "$@"` and regenerates the skill
# archive at every commit where it conflicts.
#
#   scripts/rebase.sh origin/main
#
# skills/tuios.tar.gz is generated from skills/tuios/*.md by `go generate
# ./skills`. Every pull request that edits a skill file regenerates it, so two
# such pull requests always conflict on it, and git cannot merge a gzip file.
# The archive holds nothing the Markdown files do not, so the fix is to run the
# generators again on the merged files. This script does that at each stop
# where the archive is the only conflicted path, and continues. A stop with any
# other conflicted path is a real conflict: the script leaves the rebase there
# and exits 1 for a person to resolve. After that, run `scripts/rebase.sh
# --continue`.
#
# Run it from inside the repository. Arguments go to `git rebase` as they are.
set -euo pipefail

archive=skills/tuios.tar.gz
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
	if [ "$conflicted" != "$archive" ]; then
		echo "rebase.sh: the rebase stopped. Conflicted paths:" >&2
		printf '%s\n' "${conflicted:-(none)}" >&2
		echo "rebase.sh: resolve them, then run scripts/rebase.sh --continue" >&2
		exit 1
	fi
	go generate ./skills
	git add -- "$archive" skills/tuios
	echo "rebase.sh: regenerated $archive" >&2
	if step --continue; then
		exit 0
	fi
done
# git rebase failed without a rebase in progress, such as on a bad argument.
exit 1
