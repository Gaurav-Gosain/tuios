#!/bin/sh
# Makes a README recording light without a visible change.
#
#   assets/lighten.sh demo.gif          # writes demo.avif, then shrinks demo.gif
#   assets/lighten.sh tuios.gif --gif   # shrinks the GIF only
#
# The README shows the AVIF to every browser that plays animated AVIF (all
# current ones), through <picture>, and the GIF to the rest. GitHub serves
# both from the repository and plays both.
#
# AVIF: SVT-AV1 at CRF 30, every frame and its timing kept. For demo.gif this
# is 1.5 MB against 15.9 MB, at a mean PSNR of 41.5 dB against the original.
#
# GIF: gifsicle -O3 --lossy=20, which drops detail no eye finds in a screen
# recording. For demo.gif this is 11.6 MB, at a mean PSNR of 39.7 dB. Run it
# on the original only once: a second pass loses detail again.
#
# Needs ffmpeg with libsvtav1, and gifsicle 1.92 or newer for --lossy. Both
# run at the lowest priority, because an encode takes every core it can get.
set -eu

gif=${1:-}
mode=${2:-}
case $gif in
*.gif) ;;
*) echo "lighten.sh: give a .gif file" >&2; exit 1 ;;
esac
[ -f "$gif" ] || { echo "lighten.sh: no file $gif" >&2; exit 1; }

if [ "$mode" != "--gif" ]; then
	command -v ffmpeg >/dev/null 2>&1 || { echo "lighten.sh: no ffmpeg on PATH" >&2; exit 1; }
	SVT_LOG=1 nice -n 19 ffmpeg -v error -y -i "$gif" -c:v libsvtav1 -preset 5 -crf 30 \
		-pix_fmt yuv420p -svtav1-params tune=0 -fps_mode passthrough "${gif%.gif}.avif"
	echo "wrote ${gif%.gif}.avif"
fi

command -v gifsicle >/dev/null 2>&1 || { echo "lighten.sh: no gifsicle on PATH, left $gif as it is" >&2; exit 0; }
tmp="$gif.tmp"
nice -n 19 gifsicle -O3 --lossy=20 "$gif" -o "$tmp"
mv "$tmp" "$gif"
echo "shrank $gif"
