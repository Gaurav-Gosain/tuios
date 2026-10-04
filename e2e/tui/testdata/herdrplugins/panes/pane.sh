#!/bin/sh
# Records the environment a plugin pane gets, shows a marker, and waits.
d="$HERDR_PLUGIN_STATE_DIR"
{
	echo "entry=$HERDR_PLUGIN_ENTRYPOINT_ID"
	echo "id=$HERDR_PLUGIN_ID"
	echo "pane=$HERDR_PANE_ID"
	echo "cwd=$(pwd)"
	echo "tuios_pane=$TUIOS_PANE_ID"
} >"$d/pane-$HERDR_PLUGIN_ENTRYPOINT_ID.env"
echo "PLUGIN-PANE-READY $HERDR_PLUGIN_ENTRYPOINT_ID"
exec sleep 600
