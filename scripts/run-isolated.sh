#!/bin/sh
# Starts tuios-gpui against a private tuios daemon and prints its PID.
# TUIOS points at the tuios binary built from the gui-bridge branch.
set -e
ROOT=$(cd "$(dirname "$0")/.." && pwd)
TUIOS=${TUIOS:-$HOME/.cache/agent-tmp/tuios-gpui-test/tuios}
BASE=${BASE:-$HOME/.cache/agent-tmp/tuios-gpui-env}
BIN=${BIN:-$ROOT/target/debug/tuios-gpui}
LOG=${LOG:-$HOME/.cache/agent-tmp/gpui/run/app.log}
mkdir -p "$(dirname "$LOG")"
nohup "$BIN" --tuios "$TUIOS" --isolate "$BASE" "$@" >"$LOG" 2>&1 &
echo $!
