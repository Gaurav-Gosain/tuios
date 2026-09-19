#!/bin/sh
# Build this checkout and put it on this machine and on every host in the
# [hosts] config table, then restart each daemon so the new code is running.
#
# Sessions come back through resurrection: the daemon writes its state before
# it stops and rebuilds the layout on start. The shells under them are new, so
# anything still running in a pane ends. That is the cost of a restart and it
# is why this is a script you run rather than something that happens on a save.
#
# Usage: scripts/deploy-dev.sh [host ...]
#   With no arguments it does this machine and the hosts listed below.
set -e

BIN="${TUIOS_INSTALL_DIR:-$HOME/.local/bin}/tuios"
HOSTS="${*:-ente forgejo}"

echo "building"
go build -o /tmp/tuios-local ./cmd/tuios
GOOS=linux GOARCH=amd64 go build -o /tmp/tuios-linux ./cmd/tuios

# The far side first, so a failure there happens before this machine's daemon
# is taken down.
for h in $HOSTS; do
	echo "deploying to $h"
	scp -q /tmp/tuios-linux "$h:/tmp/tuios-new"
	# The bracket keeps the remote shell's own command line from matching the
	# pattern, which would make pkill kill the shell running it.
	ssh -o BatchMode=yes "$h" '
		set -e
		install -m 755 /tmp/tuios-new "$HOME/.local/bin/tuios"
		rm -f /tmp/tuios-new
		pkill -f "tuios[ ]daemon" || true
		sleep 1
		setsid "$HOME/.local/bin/tuios" daemon >/tmp/tuios-daemon.log 2>&1 </dev/null &
		sleep 1
	'
	echo "  $(ssh -o BatchMode=yes "$h" '~/.local/bin/tuios --version | head -1')"
done

echo "installing here"
install -m 755 /tmp/tuios-local "$BIN"
pkill -f "tuios[ ]daemon" || true
sleep 1
"$BIN" daemon >/tmp/tuios-daemon.log 2>&1 </dev/null &
sleep 1
echo "  $("$BIN" --version | head -1)"
echo "done"
