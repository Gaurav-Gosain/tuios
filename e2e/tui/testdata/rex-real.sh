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
mkdir -p "$home/.local/bin" "$home/.config/rex" "$home/.config/tuios" "$tmp/state" "$tmp/run" "$tmp/agents"
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
	[ -n "${ev:-}" ] && kill "$ev" 2>/dev/null
	rx server stop >/dev/null 2>&1
	[ -n "${srv:-}" ] && kill "$srv" 2>/dev/null
	tu kill-server >/dev/null 2>&1
	rm -rf "$tmp"
}
trap cleanup EXIT

# A stand-in agent with an approval prompt, and the harness rule that reads
# it and answers y or n, for "answer an agent". respond_from_shell lets the
# Rex server, a process outside every pane, answer for the person; the
# daemon reads it at start.
mkdir -p "$home/.config/tuios/harnesses"
printf '[daemon]\nrespond_from_shell = true\n' > "$home/.config/tuios/config.toml"
cat > "$home/.config/tuios/harnesses/rexapprove.toml" <<'EOS'
schema_version = 1
id = "rexapprove"
[detect]
comm  = ["rexapprove"]
argv0 = ["rexapprove"]
[screen]
enabled = true
[[screen.rule]]
state    = "needs_input"
priority = 30
message  = "Run make?"
all      = ["Allow?"]
[screen.rule.answers]
approve = { keys = ["y"] }
deny = { keys = ["n"] }
EOS
cat > "$tmp/agents/approve.sh" <<'EOS'
#!/bin/sh
stty raw -echo
printf 'make\r\nAllow?\r\n'
c=$(dd bs=1 count=1 2>/dev/null)
printf 'ANSWER=%s\r\n' "$c"
exec sleep 600
EOS

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
rx actions > "$out/rex-actions.txt" 2>&1
echo "tuios actions in Rex: $(grep -c '^tuios_' "$out/rex-actions.txt")"
echo "tuios actions --json: $(tu actions --json | grep -o '"name":' | wc -l | tr -d ' ') entries"
rx actions --json > "$out/rex-actions.json" 2>&1
grep -E '^tuios_(prefix_inbox|goto_picker|start_agent|check|new_window|prefix_selection) ' "$out/rex-actions.txt"

step "a Rex session whose block runs the tuios client"
rx new --json > "$out/rex-new.json" 2>&1; cat "$out/rex-new.json"
rx run --json -- "$home/.local/bin/tuios" attach demo > "$out/rex-run.json" 2>&1; cat "$out/rex-run.json"
blk=$(grep -o '"block:[^"]*"' "$out/rex-run.json" | head -1 | tr -d '"')
echo "block $blk"
for _ in $(seq 50); do tu list-clients --json 2>/dev/null | grep -q demo && break; sleep 0.2; done
tu list-clients --json > "$out/tuios-clients.json" 2>&1

n=0
act() {
	n=$((n + 1))
	f=$(printf '%02d-%s' "$n" "$1")
	step "rex do $*"
	rx do -b "$blk" "$@" > "$out/do-$f.txt" 2>&1; echo "rc=$?"; cat "$out/do-$f.txt"
	for _ in $(seq 10); do sleep 0.2; done
	rx capture -b "$blk" > "$out/capture-$f.txt" 2>&1
	tu capture-pane -s demo > "$out/tuios-$f.txt" 2>&1
	# Close what the action opened, so the next one starts from the panes.
	rx send-key -b "$blk" escape > /dev/null 2>&1; sleep 0.3
	rx send-key -b "$blk" escape > /dev/null 2>&1; sleep 0.3
}
act tuios_check
act tuios_prefix_inbox
act tuios_goto_picker
act tuios_prefix_mail
act tuios_prefix_next_attention
act tuios_new_window
act tuios_layout_bsp
act tuios_prefix_selection
act tuios_start_agent
act tuios_start_agent harness=standin name=rexdemo
act tuios_start_agent harness=codex name=enumcheck prompt=hi folder=/tmp
tu list-agents -s demo > "$out/tuios-agents.txt" 2>&1; cat "$out/tuios-agents.txt"
act tuios_start_agent harness=nosuchagent name=missing
if [ -x "$claudedir/claude" ]; then
	act tuios_start_agent harness=claude name=claude-from-rex
	tu list-windows -s demo > "$out/tuios-windows.txt" 2>&1; cat "$out/tuios-windows.txt"
fi

step "a key by a version 2 name, and one for no action"
printf 'require("tuios").setup{ keys = { tuios_agents = "cmd+shift+f12", tuios_nope = "cmd+shift+f11" } } -- added by tuios integration\n' > "$cfg.new"
grep -v 'added by tuios integration' "$cfg" >> "$cfg.new"; cp "$cfg" "$out/init-before-keys.lua"; mv "$cfg.new" "$cfg"
rx config check > "$out/config-check-keys.txt" 2>&1; cat "$out/config-check-keys.txt"
rx config reload > /dev/null 2>&1
rx keymap > "$out/keymap.txt" 2>&1; grep -i 'tuios' "$out/keymap.txt"
act tuios_check
cp "$out/init-before-keys.lua" "$cfg"; rx config reload > /dev/null 2>&1

step "the suggested keys"
printf 'require("tuios").setup{ keys = "suggested" } -- added by tuios integration\n' > "$cfg.new"
grep -v 'added by tuios integration' "$cfg" >> "$cfg.new"; mv "$cfg.new" "$cfg"
rx config check > "$out/config-check-suggested.txt" 2>&1; echo "config check rc=$?"; cat "$out/config-check-suggested.txt"
rx config reload > /dev/null 2>&1
rx keymap > "$out/keymap-suggested.txt" 2>&1; echo "keymap rc=$?"; grep -i 'tuios' "$out/keymap-suggested.txt"
grep -i -E 'warn|error|complain|unknown' "$out/keymap-suggested.txt"
rx keymap --json > "$out/keymap-suggested.json" 2>&1
cp "$out/init-before-keys.lua" "$cfg"; rx config reload > /dev/null 2>&1

# Agents that wait: tuios writes OSC 9 for an agent that needs the person,
# and Rex raises terminal.desktop_notification for it. None is written for the
# pane the person looks at: the focused tuios pane while Rex shows the tuios
# block, which Rex tells tuios with focus events (DECSET 1004).
step "agents that wait"
rx events --all --json > "$out/events.jsonl" 2>&1 &
ev=$!
sleep 1
rx block call -b "$blk" modes > "$out/rex-modes.txt" 2>&1
grep -o '"name":"focus_event"[^}]*' "$out/rex-modes.txt"
tu list-windows -s demo --json > "$out/wait-windows.json" 2>&1
focused=$(python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); print(d["focused_window_id"])' "$out/wait-windows.json")
other=$(python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); print([w["window_id"] for w in d["windows"] if not w["focused"]][0])' "$out/wait-windows.json")
echo "focused tuios pane $focused, another $other"
mark() { echo "-- $1" >> "$out/wait-steps.txt"; tu session-info -s demo --json | grep -o '"host_focus": *"[a-z]*"' >> "$out/wait-steps.txt"; }
ask() { tu set-agent-state needs_input -s demo -w "$1" --kind question -m "$2" >/dev/null 2>&1; sleep 3; tu set-agent-state working -s demo -w "$1" >/dev/null 2>&1; sleep 1; }
mark "another pane asks while Rex shows tuios: a notification"
ask "$other" "wait-1 another pane"
mark "the focused pane asks while Rex shows tuios: none"
ask "$focused" "wait-2 focused pane, tuios shown"
rx split -b "$blk" --json -- /bin/sleep 600 > "$out/wait-split.json" 2>&1
sleep 2
mark "the focused pane asks while Rex shows another block: a notification"
ask "$focused" "wait-3 focused pane, another block shown"
rx focus "$blk" > /dev/null 2>&1
sleep 2
mark "back on the tuios block, the focused pane asks: none"
ask "$focused" "wait-4 focused pane, tuios shown again"
rx block call -b "$blk" program_status > "$out/rex-program-status.txt" 2>&1
kill "$ev" 2>/dev/null; ev=
cat "$out/wait-steps.txt"
grep -o '"name":"desktop_notification","payload":{[^}]*}' "$out/events.jsonl" > "$out/notifications.txt"
echo "Rex desktop notifications:"; cat "$out/notifications.txt"
for n in 1 3; do grep -q "wait-$n " "$out/notifications.txt" && echo "wait-$n notified, as it should" || echo "wait-$n NOT notified"; done
for n in 2 4; do grep -q "wait-$n " "$out/notifications.txt" && echo "wait-$n notified, and should not be" || echo "wait-$n not notified, as it should"; done

# Per-pane status: with host_program_status = "panes" Rex holds one record
# per tuios pane with an agent, read back through the block's program_status
# method. The option is read when a client starts, so a second client, in a
# Rex block of its own, starts after a config.d file sets it.
step "per-pane status"
rx block call -b "$blk" program_status > "$out/rex-records-session.txt" 2>&1
mkdir -p "$home/.config/tuios/config.d"
printf '[agents]\nhost_program_status = "panes"\n' > "$home/.config/tuios/config.d/panes.toml"
rx run --json -- "$home/.local/bin/tuios" attach demo > "$out/rex-run-panes.json" 2>&1
blk2=$(grep -o '"block:[^"]*"' "$out/rex-run-panes.json" | head -1 | tr -d '"')
sleep 4
tu set-agent-state needs_input -s demo -w "$other" --kind approval -m "panes-1 approve" >/dev/null 2>&1
sleep 2
rx block call -b "$blk2" program_status > "$out/rex-records-panes.txt" 2>&1
tu set-agent-state working -s demo -w "$other" >/dev/null 2>&1
echo "records, session mode:"; grep -o '"id": *"[^"]*"' "$out/rex-records-session.txt"
echo "records, panes mode:"; grep -o '"id": *"[^"]*"' "$out/rex-records-panes.txt"
env "${xdg[@]}" PATH="/usr/bin:/bin:$(dirname "$rexbin")" REX_CONFIG="$cfg" REX_SERVER="$server" "$bin" integration status rex 2>&1 | grep 'Status in Rex'

# Answer an agent: tuios_answer lists what waits, reads the prompt and
# sends the answer with tuios respond. This server has no Rex app, so a run
# with no args gets no_client from rex.ui.pick and sends nothing. A run with
# args asks nothing: a stale prompt id is refused, the current one answers.
step "answer an agent"
tu new-window -s demo approver --no-focus -- sh "$tmp/agents/approve.sh" > /dev/null 2>&1
for _ in $(seq 50); do tu capture-pane -s demo -w approver 2>/dev/null | grep -q 'Allow?' && break; sleep 0.1; done
tu set-agent-state needs_input -s demo -w approver --kind approval --harness rexapprove -m "Run make?" > /dev/null 2>&1
tu peek-prompt --json -s demo -w approver > "$out/answer-peek.json" 2>&1
pid=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["prompt_id"])' "$out/answer-peek.json")
echo "prompt id $pid, actions: $(python3 -c 'import json,sys; print(",".join(json.load(open(sys.argv[1]))["actions"]))' "$out/answer-peek.json")"
timeout 30 env "${xdg[@]}" PATH="/usr/bin:/bin" REX_CONFIG="$cfg" REX_SERVER="$server" "$rexbin" -S "$server" do -b "$blk" tuios_answer > "$out/answer-no-app.txt" 2>&1
echo "no app: rc=$?"; cat "$out/answer-no-app.txt"
rx do -b "$blk" tuios_answer window=approver session=demo answer=approve prompt_id=0000000000000000 > "$out/answer-stale.txt" 2>&1
echo "stale prompt id: rc=$?"; cat "$out/answer-stale.txt"
tu capture-pane -s demo -w approver > "$out/answer-before.txt" 2>&1
rx do -b "$blk" tuios_answer window=approver session=demo answer=approve prompt_id="$pid" > "$out/answer-sent.txt" 2>&1
echo "answer: rc=$?"; cat "$out/answer-sent.txt"
for _ in $(seq 50); do tu capture-pane -s demo -w approver 2>/dev/null | grep -q 'ANSWER=' && break; sleep 0.1; done
tu capture-pane -s demo -w approver > "$out/answer-after.txt" 2>&1
grep -q 'ANSWER=' "$out/answer-before.txt" && echo "the agent had an answer before Rex sent one" || echo "no answer before Rex sent one, as it should"
grep -o 'ANSWER=.' "$out/answer-after.txt" || echo "the agent got NO answer"

# Switch or attach a session: with args it asks nothing. The client in
# the tuios block switches to the session and back. From a block with no
# tuios client, a new Rex window runs tuios attach. With no args and no Rex
# app, the picker gets no_client.
step "switch or attach a session"
tu new other --detach > /dev/null 2>&1
clientpid=$(python3 -c 'import json,sys; print([c["pid"] for c in json.load(open(sys.argv[1])) if c.get("session") == "demo"][0])' "$out/tuios-clients.json")
timeout 30 env "${xdg[@]}" PATH="/usr/bin:/bin" REX_CONFIG="$cfg" REX_SERVER="$server" "$rexbin" -S "$server" do -b "$blk" tuios_switch_session > "$out/switch-no-app.txt" 2>&1
echo "no app: rc=$?"; cat "$out/switch-no-app.txt"
rx do -b "$blk" tuios_switch_session session=other > "$out/switch-other.txt" 2>&1
echo "switch: rc=$?"; cat "$out/switch-other.txt"
for _ in $(seq 50); do tu list-clients --json 2>/dev/null | python3 -c 'import json,sys; sys.exit(0 if any(c["pid"] == int(sys.argv[1]) and c.get("session") == "other" for c in json.load(sys.stdin)) else 1)' "$clientpid" && break; sleep 0.1; done
tu list-clients --json > "$out/switch-clients.json" 2>&1
python3 -c 'import json,sys; print("the Rex block client shows", [c.get("session") for c in json.load(open(sys.argv[1])) if c["pid"] == int(sys.argv[2])])' "$out/switch-clients.json" "$clientpid"
rx do -b "$blk" tuios_switch_session session=demo > "$out/switch-back.txt" 2>&1; echo "switch back: rc=$?"
rx split -b "$blk" --json -- /bin/sleep 600 > "$out/switch-split.json" 2>&1
blk3=$(grep -o '"block:[^"]*"' "$out/switch-split.json" | tr -d '"' | grep -vx "$blk" | head -1)
echo "block with no tuios: $blk3"
before=$(tu list-clients --json | python3 -c 'import json,sys; print(sum(1 for c in json.load(sys.stdin) if c.get("session") == "other"))')
rx do -b "$blk3" tuios_switch_session session=other > "$out/switch-attach.txt" 2>&1
echo "attach from a block with no tuios: rc=$?"; cat "$out/switch-attach.txt"
for _ in $(seq 50); do n=$(tu list-clients --json | python3 -c 'import json,sys; print(sum(1 for c in json.load(sys.stdin) if c.get("session") == "other"))'); [ "$n" -gt "$before" ] && break; sleep 0.1; done
echo "clients on other: $before before, $n after"
rx focus "$blk" > /dev/null 2>&1

step "status"
env "${xdg[@]}" PATH="/usr/bin:/bin:$(dirname "$rexbin")" REX_CONFIG="$cfg" REX_SERVER="$server" "$bin" integration status rex > "$out/status.txt" 2>&1
cat "$out/status.txt"

step "uninstall"
env "${xdg[@]}" PATH="/usr/bin:/bin:$(dirname "$rexbin")" REX_CONFIG="$cfg" REX_SERVER="$server" "$bin" integration uninstall rex > "$out/uninstall.txt" 2>&1
cat "$out/uninstall.txt"
if cmp -s "$cfg" "$out/init-before.lua"; then echo "init.lua is back as it was"; else echo "init.lua differs from before"; diff "$out/init-before.lua" "$cfg"; fi
echo REX-REAL-DONE
