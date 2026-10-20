#!/bin/sh
# Builds tuios.dev/sketch for the browser and writes the files the docs
# site's Mermaid playground (tuios.dev/sketch) needs into one directory.
#
# Usage: cmd/sketch-wasm/build.sh [--raw] [outdir]
#
#   --raw   keep the uncompressed sketch.wasm as well
#
# It writes sketch.wasm.br and sketch.wasm.gz, Go's wasm_exec.js, and
# manifest.json with the commit and the sizes. Needs go, gzip and brotli.
#
# The build uses go.work, so it compiles the sketch folder of this checkout.
# It does not change the tuios binary: only this command imports js code.
set -e
raw=0
if [ "$1" = "--raw" ]; then
	raw=1
	shift
fi
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
out=${1:-"$root/.wasm-build/sketch"}
mkdir -p "$out"
out=$(cd "$out" && pwd)
cd "$root"

# -s -w drops the symbol table and DWARF. It saves little on wasm, which
# keeps its function names, but costs nothing.
GOOS=js GOARCH=wasm go build -trimpath -ldflags "-s -w" \
	-o "$out/sketch.wasm" ./cmd/sketch-wasm
gzip -9 -n -c "$out/sketch.wasm" >"$out/sketch.wasm.gz"
brotli -q 11 -f -o "$out/sketch.wasm.br" "$out/sketch.wasm"
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" "$out/"
chmod u+w "$out/wasm_exec.js"

sha() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	else
		shasum -a 256 "$1" | cut -d' ' -f1
	fi
}
size() { wc -c <"$1" | tr -d ' '; }
commit=$(git rev-parse HEAD 2>/dev/null || echo unknown)
rawsize=$(size "$out/sketch.wasm")
if [ "$raw" = 0 ]; then
	rm -f "$out/sketch.wasm"
fi
{
	printf '{\n  "commit": "%s",\n' "$commit"
	printf '  "wasm": {"raw": %s, "gzip": %s, "brotli": %s},\n' \
		"$rawsize" "$(size "$out/sketch.wasm.gz")" "$(size "$out/sketch.wasm.br")"
	printf '  "files": {\n'
	first=1
	for f in $(cd "$out" && find . -type f ! -name manifest.json | sed 's|^\./||' | sort); do
		[ "$first" = 1 ] || printf ',\n'
		first=0
		printf '    "%s": {"size": %s, "sha256": "%s"}' "$f" "$(size "$out/$f")" "$(sha "$out/$f")"
	done
	printf '\n  }\n}\n'
} >"$out/manifest.json"

echo "sketch.wasm: raw $rawsize, gzip $(size "$out/sketch.wasm.gz"), brotli $(size "$out/sketch.wasm.br") bytes"
echo "wrote $out"
