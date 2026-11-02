#!/bin/sh
# Runs when you choose "Hello: Say hello" in the palette.
# docs/PLUGINS.md in the tuios repository lists every TUIOS_* variable.
"$TUIOS_BIN" plugin-notify "Hello from $TUIOS_PLUGIN_SESSION"
