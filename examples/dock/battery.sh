#!/bin/sh
# Dock component: battery charge and whether it is going up or down.
#
#   [dock.custom.battery]
#   command = "~/.config/tuios/dock/battery.sh"
#   refresh = "60s"
#
# A minute is the right cadence: the number moves about one point every few
# minutes, and the engine draws no frame at all on a poll whose value has not
# changed, so a faster poll would buy nothing but subprocesses.
#
# Exits silently on a machine with no battery, which hides the cell. That is the
# desktop case and the "you are on a server over SSH" case, and neither wants a
# cell reading 0%.
set -eu

capacity=""
status=""

# Linux: sysfs, whichever battery the machine calls its first.
for bat in /sys/class/power_supply/BAT*; do
	[ -r "$bat/capacity" ] || continue
	capacity=$(cat "$bat/capacity")
	[ -r "$bat/status" ] && status=$(cat "$bat/status")
	break
done

# macOS: pmset is the only thing that knows.
if [ -z "$capacity" ] && command -v pmset >/dev/null 2>&1; then
	line=$(pmset -g batt | sed -n '2p')
	capacity=$(printf '%s' "$line" | sed -n 's/.*[^0-9]\([0-9][0-9]*\)%.*/\1/p')
	case "$line" in
	*"AC attached"* | *charging*) status=Charging ;;
	*) status=Discharging ;;
	esac
fi

[ -n "$capacity" ] || exit 0

case "$status" in
Charging | Full) glyph="+" ;;
*) glyph="" ;;
esac

# The one place a colour is worth spending: a battery about to die is the rare
# dock reading that is an alarm rather than information.
if [ "$capacity" -le 20 ] && [ "$glyph" != "+" ]; then
	printf '\033[31m%s%%%s\033[0m\n' "$capacity" "$glyph"
else
	printf '%s%%%s\n' "$capacity" "$glyph"
fi
