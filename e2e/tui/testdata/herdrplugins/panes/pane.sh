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
if [ "$HERDR_PLUGIN_ENTRYPOINT_ID" = side ]; then
	# Tags the pane it was opened from, as herdr-sidebar tags each preview
	# pane it opens: a plugin's pane reports metadata for another pane.
	target=$(printf '%s' "$HERDR_PLUGIN_CONTEXT_JSON" | sed -n 's/.*"focused_pane_id":"\([^"]*\)".*/\1/p')
	"$HERDR_BIN_PATH" pane report-metadata "$target" --source e2e.panes --token e2e-mark=from-plugin-pane >"$d/mark.out" 2>&1
	echo "code=$? target=$target" >>"$d/mark.out"
fi
echo "PLUGIN-PANE-READY $HERDR_PLUGIN_ENTRYPOINT_ID"
exec sleep 600
