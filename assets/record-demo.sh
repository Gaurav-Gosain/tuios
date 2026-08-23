#!/bin/sh
# Records assets/demo.gif for the README.
#
#   assets/record-demo.sh          # uses the tuios first on $PATH
#
# The preparation has to happen out here rather than inside the tape. vhs
# applies a tape's Env from the very first frame, so its shell starts with
# XDG_CONFIG_HOME already pointing at a directory that does not exist yet, and
# fish creates it and writes a default config into it before any Type command
# could build the real thing.
#
# What it builds:
#
#   A scratch XDG root, because [startup] daemon is on by default and a
#   recording made against the real one attaches to the daemon the person
#   recording is working in, films their panes, and leaves its own behind.
#
#   A config directory of symlinks to the real one, with tuios replaced by a
#   copy. The session then looks like the machine it was recorded on - the same
#   shell, prompt and fastfetch - while the theme applied at the end of the
#   tape, which tuios persists, lands in the copy instead of the real config.
set -eu

root=/tmp/tuios-demo
here=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)

command -v tuios >/dev/null 2>&1 || { echo "record-demo.sh: no tuios on PATH" >&2; exit 1; }
command -v vhs >/dev/null 2>&1 || { echo "record-demo.sh: no vhs on PATH" >&2; exit 1; }

XDG_RUNTIME_DIR=$root/run
XDG_STATE_HOME=$root/state
XDG_DATA_HOME=$root/data
XDG_CACHE_HOME=$root/cache
XDG_CONFIG_HOME=$root/config
export XDG_RUNTIME_DIR XDG_STATE_HOME XDG_DATA_HOME XDG_CACHE_HOME XDG_CONFIG_HOME

# Stop the previous recording's daemon before clearing its state, or it is still
# holding that session and the next take opens on the last take's panes, their
# shells respawned in a working directory that no longer exists. Addressed
# through the scratch runtime directory, so it can only ever reach this demo's
# own daemon and never the one the recorder is working in.
mkdir -p "$root/run"
tuios kill-server >/dev/null 2>&1 || true
sleep 1

rm -rf "$root"
mkdir -p "$root/run" "$root/state" "$root/data" "$root/cache" "$root/work" "$root/config"

ln -sfn "$HOME"/.config/* "$root/config/"
rm -f "$root/config/tuios"
cp -r "$HOME/.config/tuios" "$root/config/tuios"

cd "$here"
vhs "$@" demo.tape
