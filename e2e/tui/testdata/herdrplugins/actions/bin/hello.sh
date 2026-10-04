#!/bin/sh
# Records the environment herdr gives an action, then prints a line.
d="$HERDR_PLUGIN_STATE_DIR"
n=$(ls "$d" | grep -c '^run-' )
f="$d/run-$n.env"
{
	echo "arg=$1"
	echo "cwd=$(pwd)"
	echo "id=$HERDR_PLUGIN_ID"
	echo "root=$HERDR_PLUGIN_ROOT"
	echo "action=$HERDR_PLUGIN_ACTION_ID"
	echo "socket=$HERDR_SOCKET_PATH"
	echo "config=$HERDR_PLUGIN_CONFIG_DIR"
	echo "pane=$HERDR_PANE_ID"
	echo "context=$HERDR_PLUGIN_CONTEXT_JSON"
	if [ -t 0 ]; then echo "stdin=tty"; else echo "stdin=none"; fi
	"$HERDR_BIN_PATH" pane list >/dev/null 2>&1 && echo "herdr=ok" || echo "herdr=fail"
} >"$f"
echo "hello from e2e.actions"
