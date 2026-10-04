#!/bin/sh
# Records that startup ran, then keeps running like a service would.
d="$HERDR_PLUGIN_STATE_DIR"
echo "$$" >"$d/startup.pid"
echo "startup $HERDR_PLUGIN_EVENT $HERDR_PLUGIN_ID" >>"$d/startup.log"
exec sleep 600
