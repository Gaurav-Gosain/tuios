#!/bin/sh
# tuios Claude Code agent-state shim, kept for settings that already point here.
#
# The reporter is built into tuios now: `tuios agent-hook claude-code` reads the
# hook payload on stdin, maps the event (see docs/AGENT_STATE.md), finds the
# pane, and reports. It needs no python3, and `tuios integration install
# claude-code` wires it into settings.json without this file.
#
# This shim only hands the payload over. It exits 0 when tuios is not on PATH,
# so it stays safe to leave wired up.

command -v tuios >/dev/null 2>&1 || exit 0
exec tuios agent-hook claude-code
