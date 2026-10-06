#!/bin/sh
# Fills a private tuios daemon with sessions, panes and agent states, for
# screenshots of the GUI. It never touches your own daemon.
#
#   scripts/demo/seed.sh /path/to/tuios ~/.cache/agent-tmp/demo-env
#   tuios-gpui --tuios /path/to/tuios --isolate ~/.cache/agent-tmp/demo-env --session tuios
set -e
TUIOS=$1
BASE=$2
[ -n "$TUIOS" ] && [ -n "$BASE" ] || { echo "usage: seed.sh TUIOS DIR" >&2; exit 2; }
HERE=$(cd "$(dirname "$0")" && pwd)
REPO=$(cd "$HERE/../.." && pwd)
for d in run config state cache data home; do mkdir -p "$BASE/$d"; done
chmod 700 "$BASE/run"
export XDG_RUNTIME_DIR="$BASE/run" XDG_CONFIG_HOME="$BASE/config" XDG_STATE_HOME="$BASE/state"
export XDG_CACHE_HOME="$BASE/cache" XDG_DATA_HOME="$BASE/data" HOME="$BASE/home" SHELL=/bin/bash
unset TUIOS_SOCKET TUIOS_WINDOW TUIOS_SESSION
T() { "$TUIOS" "$@"; }
AGENT="$HERE/fake-agent.sh"

win() { # session workspace cwd name command...
	s=$1 ws=$2 cwd=$3 name=$4; shift 4
	T new-window -s "$s" --workspace "$ws" --cwd "$cwd" --no-focus --print-id "$name" -- "$@"
}
state() { # session window state harness message [kind]
	if [ -n "$6" ]; then
		T set-agent-state "$3" -s "$1" -w "$2" --harness "$4" -m "$5" --kind "$6" >/dev/null
	else
		T set-agent-state "$3" -s "$1" -w "$2" --harness "$4" -m "$5" >/dev/null
	fi
}
first() { T ls --json | python3 -c "import json,sys; print([s for s in json.load(sys.stdin) if s['name']=='$1'][0]['windows'][0]['id'])"; }

T new -d tuios --cwd "$REPO" >/dev/null
seed=$(first tuios)
a=$(win tuios 1 "$REPO" "paint cache" "$AGENT" codex)
b=$(win tuios 1 "$REPO" "api retries" "$AGENT" claude-question)
c=$(win tuios 1 "$REPO" "" nvim -n crates/tuios-gpui/src/fleet.rs)
T close-window -s tuios "$seed" >/dev/null 2>&1 || true
d=$(win tuios 2 "$REPO" "" htop)
e=$(win tuios 3 "$REPO" "flaky test" "$AGENT" claude-done)
T set-workspace-name -s tuios 1 build >/dev/null 2>&1 || true
T set-workspace-name -s tuios 2 monitor >/dev/null 2>&1 || true
T set-workspace-name -s tuios 3 review >/dev/null 2>&1 || true

T new -d api --cwd "$BASE/home" >/dev/null
f=$(win api 1 "$BASE/home" "migrations" "$AGENT" codex-error)
g=$(win api 1 "$BASE/home" "readme" "$AGENT" claude-done)
h=$(win api 2 "$BASE/home" "docs pass" "$AGENT" codex)

T new -d dotfiles --cwd "$BASE/home" >/dev/null
win dotfiles 1 "$BASE/home" "" >/dev/null

sleep 1
state tuios "$a" working codex "Editing crates/tuios-gpui/src/painter.rs"
state tuios "$b" needs_input claude-code "Retry on 429, or surface the error?" question
state tuios "$e" done claude-code "Fixed the race in attach. 3 files changed"
state api "$f" errored codex "Migration failed: relation users already exists"
state api "$g" idle claude-code "Updated 4 docs pages with the new flags"
state api "$h" working codex "Reading docs/KEYBINDINGS.md"
T ls
