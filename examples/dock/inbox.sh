#!/bin/sh
# Dock component: unread maildir count, pushed rather than polled.
#
#   [dock.custom.inbox]
#   command = "~/.config/tuios/dock/inbox.sh"
#   refresh = "push"
#
# A push component stays running and each line it writes replaces the cell. It
# is the answer to "wake me when X changes" for anything that can tell you: you
# bring the watcher, tuios reads the pipe. No polling, and no wake at all until
# something actually happens.
#
# The shape here is the shape of every push component: emit once so the cell is
# populated straight away, then block on a watcher and emit again on each
# change. If the process exits, tuios restarts it with backoff, and after
# enough consecutive failures with no output it stops and waits for a
# `tuios refresh-dock inbox`.
set -eu

MAILDIR=${MAILDIR:-$HOME/Mail/INBOX/new}

emit() {
	[ -d "$MAILDIR" ] || return 0
	n=$(find "$MAILDIR" -type f 2>/dev/null | wc -l | tr -d ' ')
	if [ "$n" -gt 0 ]; then
		printf '\033[34m\033[0m %s\n' "$n"
	else
		# An empty line is a valid update: it clears the cell, which is how a
		# push component says "nothing to report" without exiting.
		printf '\n'
	fi
}

emit

if command -v inotifywait >/dev/null 2>&1; then
	# -m keeps watching. Every event is one line in, one emit out.
	inotifywait -q -m -e create -e delete -e moved_to -e moved_from "$MAILDIR" |
		while read -r _; do
			emit
		done
else
	# No watcher available: fall back to a slow loop rather than exiting, so the
	# cell still works. If this is your situation, refresh = "60s" is the
	# honest way to write it and lets tuios schedule it with everything else.
	while sleep 60; do
		emit
	done
fi
