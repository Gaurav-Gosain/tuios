#!/bin/sh
# Dock component: the git branch of the focused pane's directory.
#
# Refreshes on focus change rather than on a timer, because the branch only
# moves when you move: a poll would be right most of the time and would cost a
# subprocess every few seconds to be right.
#
#   [dock.custom.branch]
#   command = "~/.config/tuios/dock/git-branch.sh"
#   refresh = "event:after-focus-change,after-attach"
#
# Asking tuios where the focused pane is means the cell follows the pane you are
# looking at rather than wherever the client happened to start. Without jq, or
# without a daemon, it falls back to its own directory, which is still right for
# a session started inside the repo.
set -eu

dir=""
if command -v jq >/dev/null 2>&1; then
	dir=$(tuios list-windows --json 2>/dev/null |
		jq -r '.windows[] | select(.focused) | .cwd // empty' 2>/dev/null) || dir=""
fi
[ -n "$dir" ] || dir=$PWD

branch=$(git -C "$dir" branch --show-current 2>/dev/null) || exit 0
[ -n "$branch" ] || exit 0

# A dirty tree gets a marker rather than a colour. The bar spends its one
# saturated element on the mode pill, and a branch name is something you read
# rather than an alarm.
if [ -n "$(git -C "$dir" status --porcelain 2>/dev/null)" ]; then
	branch="$branch*"
fi

printf '\033[35m\033[0m %s\n' "$branch"
