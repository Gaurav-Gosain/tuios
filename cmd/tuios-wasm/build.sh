#!/bin/sh
# Builds tuios for the browser and writes everything the docs site's /learn
# page needs into one directory. See README.md for the list.
#
# Usage: cmd/tuios-wasm/build.sh [--raw] [outdir]
#
#   --raw   keep the uncompressed tuios.wasm as well (it is over Cloudflare's
#           25 MiB per-file limit, so the site never ships it)
#
# Needs go, gzip and brotli. The fonts are converted to woff2 with fonttools'
# pyftsubset when it, or uvx to fetch it, is on PATH; otherwise the TTFs are
# copied as they are.
set -e
raw=0
if [ "$1" = "--raw" ]; then
	raw=1
	shift
fi
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
out=${1:-"$root/.wasm-build/site"}
export GOWORK=off
modfile=$("$here/prepare.sh")
mkdir -p "$out/fonts"
out=$(cd "$out" && pwd)
cd "$root"

GOOS=js GOARCH=wasm go build -modfile "$modfile" -trimpath -ldflags "-s -w" \
	-o "$out/tuios.wasm" ./cmd/tuios-wasm
gzip -9 -n -c "$out/tuios.wasm" >"$out/tuios.wasm.gz"
brotli -q 11 -f -o "$out/tuios.wasm.br" "$out/tuios.wasm"

cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" "$out/"

# The renderer is sip's webterm bundle (xterm.js with the WebGL addon), from
# the sip version go.mod already pins. The js build does not import sip, so
# nothing has downloaded it yet on a cold module cache, and go list -m would
# print an empty directory. Download it first.
go mod download github.com/Gaurav-Gosain/sip
sip=$(go list -m -f '{{.Dir}}' github.com/Gaurav-Gosain/sip)
if [ -z "$sip" ] || [ ! -f "$sip/static/webterm.js" ]; then
	echo "build.sh: sip's webterm bundle not found (sip dir: '$sip')" >&2
	exit 1
fi
cp "$sip/static/webterm.js" "$sip/static/webterm.css" "$sip/static/xterm.css" "$out/"

if command -v pyftsubset >/dev/null 2>&1; then
	subset="pyftsubset"
elif command -v uvx >/dev/null 2>&1; then
	subset="uvx --from fonttools --with brotli pyftsubset"
else
	subset=""
fi
for face in Regular Bold; do
	src="$sip/static/fonts/JetBrainsMonoNerdFontMono-$face.ttf"
	if [ -n "$subset" ]; then
		# Every glyph and feature is kept: tuios draws nerd font icons in the
		# dock and the launcher. woff2 alone takes the file from 2.4 MB to
		# under 1 MB.
		$subset "$src" --unicodes='*' --glyphs='*' --layout-features='*' \
			--flavor=woff2 --output-file="$out/fonts/JetBrainsMonoNerdFontMono-$face.woff2" 2>/dev/null
	else
		echo "build.sh: no pyftsubset or uvx, copying the TTF" >&2
		cp "$src" "$out/fonts/"
	fi
done

if [ -d "$here/web" ]; then
	cp -R "$here/web/." "$out/"
fi
chmod -R u+w "$out"

# manifest.json: what was built, from which commit, and how big.
sha() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	else
		shasum -a 256 "$1" | cut -d' ' -f1
	fi
}
size() { wc -c <"$1" | tr -d ' '; }
commit=$(git rev-parse HEAD 2>/dev/null || echo unknown)
version=$(git describe --tags --always 2>/dev/null || echo dev)
rawsize=$(size "$out/tuios.wasm")
if [ "$raw" = 0 ]; then
	rm -f "$out/tuios.wasm"
fi
{
	printf '{\n  "version": "%s",\n  "commit": "%s",\n' "$version" "$commit"
	printf '  "wasm": {"raw": %s, "gzip": %s, "brotli": %s},\n' \
		"$rawsize" "$(size "$out/tuios.wasm.gz")" "$(size "$out/tuios.wasm.br")"
	printf '  "files": {\n'
	first=1
	for f in $(cd "$out" && find . -type f ! -name manifest.json | sed 's|^\./||' | sort); do
		[ "$first" = 1 ] || printf ',\n'
		first=0
		printf '    "%s": {"size": %s, "sha256": "%s"}' "$f" "$(size "$out/$f")" "$(sha "$out/$f")"
	done
	printf '\n  }\n}\n'
} >"$out/manifest.json"

echo "tuios.wasm: raw $rawsize, gzip $(size "$out/tuios.wasm.gz"), brotli $(size "$out/tuios.wasm.br") bytes"
echo "wrote $out"
