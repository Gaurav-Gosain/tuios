#!/bin/sh
# Dock component: how many panes are running an agent, and what they are doing.
#
#   [dock.custom.agents]
#   command  = "~/.config/tuios/dock/agents.sh"
#   refresh  = "event:after-agent-state"
#   on-click = "tuios list-windows"
#
# This is the component the whole feature is for. With several agents working in
# several panes, the one thing you want off the bar is whether any of them is
# waiting on you, and that is exactly the thing you cannot see because the pane
# is not on screen.
#
# It refreshes on the event rather than on a timer, so it costs nothing at all
# until an agent's state actually changes.
set -eu

command -v jq >/dev/null 2>&1 || exit 0

states=$(tuios list-windows --json 2>/dev/null |
	jq -r '.windows[] | .agent_state // empty' 2>/dev/null) || exit 0
[ -n "$states" ] || exit 0

working=$(printf '%s\n' "$states" | grep -c '^working$' || true)
waiting=$(printf '%s\n' "$states" | grep -c '^waiting$' || true)
done_=$(printf '%s\n' "$states" | grep -c '^done$' || true)

out=""
# Waiting comes first because it is the only one of the three that is asking
# for something. Working is progress you do not have to act on, and done is
# past tense.
[ "$waiting" -gt 0 ] && out="$out \033[33m${waiting} waiting\033[0m"
[ "$working" -gt 0 ] && out="$out ${working} working"
[ "$done_" -gt 0 ] && out="$out \033[32m${done_} done\033[0m"

[ -n "$out" ] || exit 0
# shellcheck disable=SC2059 # the accumulated string carries the escapes
printf "󰚩${out}\n"
