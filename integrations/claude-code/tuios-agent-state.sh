#!/bin/sh
# tuios Claude Code agent-state shim.
#
# Maps Claude Code lifecycle hooks to tuios per-pane agent state so a session can
# show which panes need attention. It reports:
#
#   SessionStart, UserPromptSubmit -> working
#   Notification                   -> needs_input
#   Stop                           -> done
#
# It no-ops cleanly when not running under tuios: it exits 0 the moment any of the
# environment tuios sets on a pane (TUIOS_ENV, TUIOS_SOCKET, TUIOS_PANE_ID) is
# missing, so it is safe to leave wired up outside tuios. Subagent events are
# ignored so a Task-tool subagent cannot clobber the pane's displayed state.
#
# Wiring is documented in the README beside this file. This is tuios's own shim;
# it is separate from any other integration and does not touch them.

set -eu

# Guard on tuios's pane environment. Any one missing means we are not inside a
# tuios pane (or an older tuios), so there is nothing to report to.
[ "${TUIOS_ENV:-}" = "1" ] || exit 0
[ -n "${TUIOS_SOCKET:-}" ] || exit 0
[ -n "${TUIOS_PANE_ID:-}" ] || exit 0

# The reporter and the JSON parser are both required; a missing one is a clean
# no-op rather than an error, matching how the guards above behave.
command -v tuios >/dev/null 2>&1 || exit 0
command -v python3 >/dev/null 2>&1 || exit 0

hook_input="$(cat 2>/dev/null || true)"

# Derive the state from the hook event, filtering out subagent turns. Prints an
# empty line (and the script then exits) for any event that does not map.
state="$(printf '%s' "$hook_input" | python3 - <<'PY'
import json
import sys

try:
    data = json.load(sys.stdin)
except Exception:
    data = {}

event = str(data.get("hook_event_name") or "")

# A subagent turn must never drive the pane's state: agent_id marks a subagent
# event, and SubagentStop is a subagent completion.
if data.get("agent_id") or event == "SubagentStop":
    sys.exit(0)

mapping = {
    "SessionStart": "working",
    "UserPromptSubmit": "working",
    "Notification": "needs_input",
    "Stop": "done",
}
state = mapping.get(event, "")
if state:
    print(state)
PY
)"

[ -n "$state" ] || exit 0

# A Notification carries a short message describing what it wants; pass it along
# so the pane's indicator can say why it is blocked.
message=""
if [ "$state" = "needs_input" ]; then
	message="$(printf '%s' "$hook_input" | python3 -c 'import json,sys
try:
    d = json.load(sys.stdin)
except Exception:
    d = {}
print(str(d.get("message") or ""))' 2>/dev/null || true)"
fi

# Report best-effort: a failed report must never fail the hook and interrupt the
# agent. The window is the pane id tuios exported; the session scopes the lookup.
if [ -n "$message" ]; then
	tuios set-agent-state "$state" -s "${TUIOS_SESSION:-}" -w "$TUIOS_PANE_ID" -m "$message" >/dev/null 2>&1 || true
else
	tuios set-agent-state "$state" -s "${TUIOS_SESSION:-}" -w "$TUIOS_PANE_ID" >/dev/null 2>&1 || true
fi

exit 0
