#!/bin/sh
# Draws the Direction A mockups and converts them to PNG with rsvg-convert.
# A private fontconfig adds the bundled Inter, so no font install is needed.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
fonts=$(cd "$here/../../../crates/tuios-gpui/assets/fonts" && pwd)
tmp=${TMPDIR:-/tmp}/direction-a-fc.$$
mkdir -p "$tmp"
trap 'rm -rf "$tmp"' EXIT
cat > "$tmp/fonts.conf" <<CONF
<?xml version="1.0"?>
<!DOCTYPE fontconfig SYSTEM "urn:fontconfig:fonts.dtd">
<fontconfig>
  <include ignore_missing="yes">/etc/fonts/fonts.conf</include>
  <dir>$fonts</dir>
  
</fontconfig>
CONF
export FONTCONFIG_FILE="$tmp/fonts.conf"
cd "$here"
python3 mock.py "$here"
for f in main-dark palette-dark main-light; do
  rsvg-convert -o "$f.png" "$f.svg"
done
