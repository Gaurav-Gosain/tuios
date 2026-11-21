#!/bin/sh
# Remote desktop: shows the desktop of a host from the [hosts] table in this
# pane. The host runs screen-relay. srterm runs here and draws the desktop
# with kitty graphics.
#
# The video goes through an ssh tunnel that this script starts and stops.
# When tuios forwards ports itself, the tunnel can use that instead.

TUIOS=${TUIOS_BIN:-tuios}

stop() {
	printf '%s\n' "$@"
	printf 'Press Enter to close this pane.\n'
	read -r _
	exit 1
}

command -v srterm >/dev/null 2>&1 ||
	stop "srterm is not installed." "Build it with 'go build' in the srterm folder, then put it on your PATH."
command -v ssh >/dev/null 2>&1 || stop "ssh is not installed."
command -v curl >/dev/null 2>&1 || stop "curl is not installed."

# The hosts that are up, as lines of "NAME ADDRESS".
hosts=$("$TUIOS" hosts --json 2>/dev/null | awk -F'"' '
	/^    {/ { host = ""; addr = ""; st = "" }
	/"addr":/ { addr = $4 }
	/"host":/ { host = $4 }
	/"status":/ { st = $4 }
	/^    }/ { if (host != "" && addr != "" && st == "up") print host, addr }')
[ -n "$hosts" ] ||
	stop "No host is up." "Add a host to [hosts] in config.toml, then check it with 'tuios hosts'."

host=${REMOTE_DESKTOP_HOST:-}
if [ -z "$host" ]; then
	if [ "$(printf '%s\n' "$hosts" | wc -l)" -eq 1 ]; then
		host=${hosts%% *}
	else
		set --
		for h in $(printf '%s\n' "$hosts" | cut -d' ' -f1); do
			set -- "$@" --item "$h=Remote desktop on $h"
		done
		host=$("$TUIOS" prompt pick "Remote desktop on which host?" "$@" 2>/dev/null)
		if [ -z "$host" ]; then
			# No palette: ask in the pane.
			printf 'Remote desktop on which host?\n'
			printf '%s\n' "$hosts" | cut -d' ' -f1 | awk '{ printf "  %d  %s\n", NR, $1 }'
			printf 'Type a number and press Enter: '
			read -r n
			case "$n" in '' | *[!0-9]*) stop "That is not a number." ;; esac
			host=$(printf '%s\n' "$hosts" | sed -n "${n}p" | cut -d' ' -f1)
		fi
	fi
fi
addr=$(printf '%s\n' "$hosts" | awk -v h="$host" '$1 == h { print $2 }')
[ -n "$addr" ] || stop "The host '$host' is not up."

[ -n "${TUIOS_PANE_ID:-}" ] && "$TUIOS" set-window -w "$TUIOS_PANE_ID" --name "Remote desktop on $host" >/dev/null 2>&1

printf 'Starting screen-relay on %s.\n' "$host"
sshx() { ssh -o BatchMode=yes -o ConnectTimeout=10 "$@"; }
here=$(dirname "$0")
info=$(sshx "$addr" sh -s <"$here/host.sh") || stop "ssh to $addr failed."
err=$(printf '%s\n' "$info" | sed -n 's/^error=//p')
[ -z "$err" ] || stop "$err"
rport=$(printf '%s\n' "$info" | sed -n 's/^port=//p')
token=$(printf '%s\n' "$info" | sed -n 's/^token=//p')
[ -n "$rport" ] || stop "The host did not say which port screen-relay uses."

# The tunnel: a free local port to the engine's loopback port on the host.
tunnel=""
cleanup() {
	[ -n "$tunnel" ] && kill "$tunnel" 2>/dev/null
}
trap cleanup EXIT
trap 'exit 1' INT TERM HUP

check() {
	if [ -n "$token" ]; then
		curl -fsS -m 2 -o /dev/null -H "Authorization: Bearer $token" "http://127.0.0.1:$1/capabilities"
	else
		curl -fsS -m 2 -o /dev/null "http://127.0.0.1:$1/capabilities"
	fi
}

lport=""
for try in 1 2 3 4 5; do
	p=$(awk -v s="$$$try" 'BEGIN { srand(s); print 20000 + int(rand() * 40000) }')
	# ssh itself in the background, not a function, so that $! is ssh.
	ssh -o BatchMode=yes -o ConnectTimeout=10 -o ExitOnForwardFailure=yes -N -L "127.0.0.1:$p:127.0.0.1:$rport" "$addr" &
	tunnel=$!
	i=0
	while [ $i -lt 40 ] && kill -0 "$tunnel" 2>/dev/null; do
		if check "$p" 2>/dev/null; then
			lport=$p
			break 2
		fi
		sleep 0.25
		i=$((i + 1))
	done
	kill "$tunnel" 2>/dev/null
	tunnel=""
done
[ -n "$lport" ] || stop "The ssh tunnel to $host did not start."

# When the engine does not answer, srterm asks the host why.
why=""
[ "$(sshx "$addr" uname -s 2>/dev/null)" = Darwin ] &&
	why="ssh -o BatchMode=yes -o ConnectTimeout=5 $addr sh -s <'$here/why.sh'"
SCREEN_RELAY_TOKEN=$token srterm --why "$why" "http://127.0.0.1:$lport"
status=$?
[ $status -eq 0 ] || stop "srterm stopped with status $status."
