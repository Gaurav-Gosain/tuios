#!/bin/sh
# Builds tuios for the browser into an output directory:
#   tuios.wasm, wasm_exec.js, and the page and renderer from web/.
#
# Usage: cmd/tuios-wasm/build.sh [outdir]
set -e
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
out=${1:-"$root/.wasm-build/site"}
modfile=$("$here/prepare.sh")
mkdir -p "$out"
cd "$root"
GOOS=js GOARCH=wasm go build -modfile "$modfile" -trimpath -ldflags "-s -w" \
	-o "$out/tuios.wasm" ./cmd/tuios-wasm
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" "$out/"
# The renderer is sip's vendored webterm bundle (xterm.js with the WebGL
# addon), taken from the sip version go.mod already pins.
sip=$(go list -m -f '{{.Dir}}' github.com/Gaurav-Gosain/sip)
mkdir -p "$out/fonts"
cp "$sip/static/webterm.js" "$sip/static/webterm.css" "$sip/static/xterm.css" "$out/"
cp "$sip/static/fonts/JetBrainsMonoNerdFontMono-Regular.ttf" \
	"$sip/static/fonts/JetBrainsMonoNerdFontMono-Bold.ttf" "$out/fonts/"
chmod -R u+w "$out"
if [ -d "$here/web" ]; then
	cp -R "$here/web/." "$out/"
fi
ls -l "$out/tuios.wasm"
