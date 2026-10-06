#!/bin/sh
# Takes a screenshot of tuios-gpui in a private, nested Hyprland, against a
# private tuios daemon, so it works while the desktop is locked and never
# touches your own sessions or windows.
#
#   scripts/screenshot.sh OUT.png THEME [CONTROL-COMMAND ...]
#
# Environment: TUIOS (the bridge-branch tuios), BASE (the daemon's private
# directory, seeded with scripts/demo/seed.sh), SESSION (default tuios),
# WAIT (seconds before the shot, default 3), KEEP=1 leaves Hyprland running.
set -e
OUT=$1
THEME=$2
shift 2
ROOT=$(cd "$(dirname "$0")/.." && pwd)
TUIOS=${TUIOS:?set TUIOS}
BASE=${BASE:?set BASE}
WORK=${WORK:-$HOME/.cache/agent-tmp/tuios-gpui-shot}
mkdir -p "$WORK"

# One nested Hyprland, reused between shots.
if [ -f "$WORK/hl.pid" ] && kill -0 "$(cat "$WORK/hl.pid")" 2>/dev/null; then
	:
else
	cat >"$WORK/hl.lua" <<'CONF'
hl.config({
    general = { gaps_in = 0, gaps_out = 0, border_size = 0 },
    decoration = { rounding = 0, shadow = { enabled = false }, blur = { enabled = false } },
    animations = { enabled = false },
    cursor = { invisible = true },
    debug = { suppress_errors = true },
    misc = {
        disable_hyprland_logo = true,
        disable_splash_rendering = true,
        force_default_wallpaper = 0,
        disable_watchdog_warning = true,
    },
})
CONF
	nohup nice -n 10 Hyprland -c "$WORK/hl.lua" >"$WORK/hl.log" 2>&1 &
	echo $! >"$WORK/hl.pid"
	sleep 3
fi
HLPID=$(cat "$WORK/hl.pid")
INST=$(hyprctl instances -j | python3 -c "import json,sys; print([i['instance'] for i in json.load(sys.stdin) if i['pid']==$HLPID][0])")
SOCK=$(hyprctl instances -j | python3 -c "import json,sys; print([i['wl_socket'] for i in json.load(sys.stdin) if i['pid']==$HLPID][0])")
# The nested output takes the host window's size at start; set it here.
hyprctl -i "$INST" eval "hl.monitor({ output = \"WAYLAND-1\", mode = \"${SIZE:-1440x900}@60\", position = \"0x0\", scale = 1 })" >/dev/null

WAYLAND_DISPLAY=$SOCK nohup "$ROOT/target/release/tuios-gpui" --tuios "$TUIOS" --isolate "$BASE" \
	--session "${SESSION:-tuios}" --theme "$THEME" --control "$WORK/ctl.sock" >"$WORK/app.log" 2>&1 &
APP=$!
sleep "${WAIT:-3}"
for c in "$@"; do
	case "$c" in
	sleep:*) sleep "${c#sleep:}" ;;
	*) python3 "$ROOT/scripts/ctl.py" "$WORK/ctl.sock" "$c" >/dev/null ;;
	esac
done
sleep 0.6
WAYLAND_DISPLAY=$SOCK grim -o WAYLAND-1 "$OUT"
kill "$APP"
wait "$APP" 2>/dev/null || true
if [ -z "$KEEP" ]; then
	kill "$HLPID"
	rm -f "$WORK/hl.pid"
fi
echo "$OUT"
