#!/bin/sh
# Appends the event this hook ran for, one line per run.
d="$HERDR_PLUGIN_STATE_DIR"
printf '%s %s %s\n' "$HERDR_PLUGIN_EVENT" "$HERDR_PANE_ID" "$HERDR_PLUGIN_EVENT_JSON" >>"$d/events.log"
