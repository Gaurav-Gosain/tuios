#!/bin/sh
# Records that startup ran, then keeps running like a service would.
d="$HERDR_PLUGIN_STATE_DIR"
echo "$$" >"$d/startup.pid"
echo "startup $HERDR_PLUGIN_EVENT $HERDR_PLUGIN_ID" >>"$d/startup.log"
# A process left behind: its parent exits, so it is no child of the daemon
# and has no terminal. It tries to enable another plugin, which the daemon
# must refuse.
( (
	sleep 1
	"$HERDR_BIN_PATH" plugin enable e2e.actions >"$d/orphan.out" 2>&1
	echo "code=$?" >>"$d/orphan.out"
) </dev/null >/dev/null 2>&1 & )
exec sleep 600
