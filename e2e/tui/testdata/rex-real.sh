#!/bin/bash
# rex-real.sh TUIOS_BIN OUT_DIR
#
# The Rex integration in a real Rex server of its own: a scratch server with a
# bare PATH, as the app gives one, its own init.lua and state, and a scratch
# tuios daemon under a temporary HOME. The person's Rex server, config and
# sessions, and their tuios daemon, are never used. Every step's output is
# kept under OUT_DIR.
set -u
# Run from a tuios pane or a Rex block, the caller's TUIOS_SOCKET, TUIOS_SESSION,
# REX_SERVER and the rest name the person's daemon and Rex server. Drop them
# all, so every command below reaches only the scratch ones.
for v in $(env | grep -oE '^(TUIOS|REX)_[A-Z0-9_]*'); do unset "$v"; done
bin=$(cd "$(dirname "$1")" && pwd)/$(basename "$1")
out=$(mkdir -p "$2" && cd "$2" && pwd)
rexbin=$(command -v rex) || { echo "rex is not installed"; exit 1; }
tmp=$(mktemp -d /tmp/rexreal.XXXXXX)
home=$tmp/home
mkdir -p "$home/.local/bin" "$home/.config/rex" "$tmp/state" "$tmp/run" "$tmp/agents"
chmod 700 "$tmp/run"
cd "$tmp"
# tuios where scripts/install.sh puts it, and nowhere on the server's PATH.
ln -s "$bin" "$home/.local/bin/tuios"
# A stand-in agent only on the tuios daemon's PATH.
cat > "$tmp/agents/standin" <<'EOS'
#!/bin/sh
echo "standin agent running, PATH=$PATH"
exec sleep 600
EOS
chmod +x "$tmp/agents/standin"

xdg=(HOME="$home" USER="$USER" XDG_CONFIG_HOME="$home/.config" XDG_RUNTIME_DIR="$tmp/run"
	XDG_STATE_HOME="$home/.local/state" XDG_DATA_HOME="$home/.local/share" XDG_CACHE_HOME="$home/.cache")
cfg=$home/.config/rex/init.lua
sock=$tmp/rex.sock
server="unix://$sock"
# The daemon's PATH: the stand-in, and the folder claude is installed in when
# it is, as a login shell would give it. Rex's own PATH has neither.
claudedir=$(dirname "$(command -v claude 2>/dev/null || echo /nonexistent/claude)")
tu() { env "${xdg[@]}" PATH="$tmp/agents:$claudedir:/usr/bin:/bin" "$bin" "$@"; }
rx() { env "${xdg[@]}" PATH="/usr/bin:/bin" REX_CONFIG="$cfg" REX_SERVER="$server" "$rexbin" -S "$server" "$@"; }
step() { echo "== $1"; }
cleanup() {
	rx server stop >/dev/null 2>&1
	[ -n "${srv:-}" ] && kill "$srv" 2>/dev/null
	tu kill-server >/dev/null 2>&1
	rm -rf "$tmp"
}
trap cleanup EXIT

step "the person's own init.lua"
printf -- '-- a line of the person'"'"'s own\n' > "$cfg"
cp "$cfg" "$out/init-before.lua"

step "scratch tuios daemon, with the stand-in agent on its PATH"
tu new demo --detach > "$out/tuios-new.txt" 2>&1; echo "rc=$?"

step "scratch Rex server, PATH=/usr/bin:/bin"
env -i "${xdg[@]}" PATH=/usr/bin:/bin REX_CONFIG="$cfg" "$rexbin" server run --listen "$server" \
	--config "$cfg" --state-dir "$tmp/state" --restore=false > "$out/rex-server.log" 2>&1 &
srv=$!
for _ in $(seq 50); do [ -S "$sock" ] && break; sleep 0.1; done
[ -S "$sock" ] || { echo "the Rex server did not start"; cat "$out/rex-server.log"; exit 1; }

step "tuios integration install rex, before any Rex session"
env "${xdg[@]}" PATH="/usr/bin:/bin:$(dirname "$rexbin")" REX_CONFIG="$cfg" REX_SERVER="$server" "$bin" integration install rex > "$out/install.txt" 2>&1
echo "rc=$?"; cat "$out/install.txt"
cp "$cfg" "$out/init-after-install.lua"
rx actions > "$out/rex-actions.txt" 2>&1; grep tuios "$out/rex-actions.txt"

step "a Rex session whose block runs the tuios client"
rx new --json > "$out/rex-new.json" 2>&1; cat "$out/rex-new.json"
rx run --json -- "$home/.local/bin/tuios" attach demo > "$out/rex-run.json" 2>&1; cat "$out/rex-run.json"
blk=$(grep -o '"block:[^"]*"' "$out/rex-run.json" | head -1 | tr -d '"')
echo "block $blk"
for _ in $(seq 50); do tu list-clients --json 2>/dev/null | grep -q demo && break; sleep 0.2; done
tu list-clients --json > "$out/tuios-clients.json" 2>&1

act() {
	step "rex do $*"
	rx do -b "$blk" "$@" > "$out/do-$1.txt" 2>&1; echo "rc=$?"; cat "$out/do-$1.txt"
	for _ in $(seq 10); do sleep 0.2; done
	rx capture -b "$blk" > "$out/capture-$1.txt" 2>&1
	tu capture-pane -s demo > "$out/tuios-$1.txt" 2>&1
}
act tuios_check
act tuios_agents
tu run-command -s demo Action prefix_inbox >/dev/null 2>&1
act tuios_mail
act tuios_next_waiting
act tuios_start_agent harness=standin name=rexdemo
tu list-agents -s demo > "$out/tuios-agents.txt" 2>&1; cat "$out/tuios-agents.txt"
act tuios_start_agent harness=nosuchagent name=missing
if [ -x "$claudedir/claude" ]; then
	act tuios_start_agent name=claude-from-rex
	tu list-windows -s demo > "$out/tuios-windows.txt" 2>&1; cat "$out/tuios-windows.txt"
fi

step "status"
env "${xdg[@]}" PATH="/usr/bin:/bin:$(dirname "$rexbin")" REX_CONFIG="$cfg" REX_SERVER="$server" "$bin" integration status rex > "$out/status.txt" 2>&1
cat "$out/status.txt"

step "uninstall"
env "${xdg[@]}" PATH="/usr/bin:/bin:$(dirname "$rexbin")" REX_CONFIG="$cfg" REX_SERVER="$server" "$bin" integration uninstall rex > "$out/uninstall.txt" 2>&1
cat "$out/uninstall.txt"
if cmp -s "$cfg" "$out/init-before.lua"; then echo "init.lua is back as it was"; else echo "init.lua differs from before"; diff "$out/init-before.lua" "$cfg"; fi
echo REX-REAL-DONE
