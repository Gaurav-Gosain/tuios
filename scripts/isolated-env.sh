#!/bin/sh
# Prints environment assignments that give tuios a private daemon: its own
# runtime directory (and so its own socket), config, state and HOME. Use it so
# a test never reaches the daemon you work in.
#
#   eval "$(scripts/isolated-env.sh)"            # in sh or bash
#   scripts/isolated-env.sh | source             # in fish, after editing
#
# BASE defaults to ~/.cache/agent-tmp/tuios-gpui-env.
BASE=${BASE:-$HOME/.cache/agent-tmp/tuios-gpui-env}
for d in run config state cache data home; do mkdir -p "$BASE/$d"; done
chmod 700 "$BASE/run"
cat <<EOT
export XDG_RUNTIME_DIR="$BASE/run"
export XDG_CONFIG_HOME="$BASE/config"
export XDG_STATE_HOME="$BASE/state"
export XDG_CACHE_HOME="$BASE/cache"
export XDG_DATA_HOME="$BASE/data"
export HOME="$BASE/home"
export SHELL=/bin/bash
unset TUIOS_SOCKET
EOT
