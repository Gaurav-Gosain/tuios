#!/bin/sh
# Show the pull request of the focused pane's branch in the dock. A hook runs
# this on each focus and exits, so nothing stays in memory. gh runs at most
# once a minute for one branch. Other runs reuse the saved text.

show() {
	if [ -n "$1" ]; then
		"$TUIOS_BIN" plugin-cell set status "$1" --action open
	else
		"$TUIOS_BIN" plugin-cell clear status
	fi
}

# A run in a folder with no repository or no branch shows nothing.
cd "$TUIOS_PLUGIN_CWD" 2>/dev/null || { show ""; exit 0; }
root=$(git rev-parse --show-toplevel 2>/dev/null) || { show ""; exit 0; }
branch=$(git symbolic-ref --short HEAD 2>/dev/null) || { show ""; exit 0; }

if [ "$1" = open ]; then
	exec gh pr view --web
fi

saved="$TUIOS_PLUGIN_STATE_DIR/$(printf '%s %s' "$root" "$branch" | cksum | cut -d' ' -f1)"
if [ "$1" != refresh ] && [ -n "$(find "$saved" -mmin -1 2>/dev/null)" ]; then
	show "$(cat "$saved")"
	exit 0
fi

command -v gh >/dev/null 2>&1 || { show ""; exit 0; }

# No pull request, or no gh login, is an empty text, and that is saved too.
text=$(gh pr view --json number,state,statusCheckRollup --jq '
	[.statusCheckRollup // [] | .[]
		| if (.conclusion // "") != "" then .conclusion else (.state // .status // "") end] as $checks
	| "#\(.number) " + (
		if .state != "OPEN" then (.state | ascii_downcase)
		elif any($checks[]; . == "FAILURE" or . == "ERROR" or . == "TIMED_OUT" or . == "CANCELLED") then "failing"
		elif any($checks[]; . == "PENDING" or . == "QUEUED" or . == "IN_PROGRESS" or . == "") then "running"
		elif ($checks | length) == 0 then "no checks"
		else "ok" end)' 2>/dev/null)
printf '%s' "$text" > "$saved"
show "$text"
