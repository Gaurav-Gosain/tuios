#!/bin/sh
# Build the static libghostty-vt that the ghostty build tag links against.
#
# Usage: scripts/ghostty-lib.sh [target]
#   target: zig target triple (default: native). Examples:
#     x86_64-linux-gnu, aarch64-linux-gnu, x86_64-macos, aarch64-macos,
#     x86_64-windows-gnu, aarch64-windows-gnu
#
# Output: .ghostty-vt/<target>/{lib,include,pkgconfig}. Point PKG_CONFIG_PATH
# at the pkgconfig dir and build tuios with -tags ghostty.
#
# The pkgconfig file is rewritten rather than taken from the ghostty build:
# upstream emits the bare library path in Libs:, which Go's cgo flag
# allowlist rejects, and names the windows archive ghostty-vt-static.lib,
# which -l cannot find. Both are fixed by installing as libghostty-vt.a and
# emitting -L/-l flags.
set -eu

# Pinned because the ghostty tag is a release artifact: the emulator must not
# drift under a release without a deliberate bump here.
GHOSTTY_COMMIT=99d7b5fd508eededf2de08ca641f2d83027631f8

TARGET="${1:-native}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CACHE="${GHOSTTY_VT_CACHE:-$ROOT/.ghostty-vt}"
SRC="$CACHE/src"
OUT="$CACHE/$TARGET"

command -v zig >/dev/null || { echo "ghostty-lib.sh: zig not found (need zig >= 0.16)" >&2; exit 1; }

if [ ! -d "$SRC/.git" ]; then
    git clone --filter=blob:none https://github.com/ghostty-org/ghostty "$SRC"
fi
if [ "$(git -C "$SRC" rev-parse HEAD)" != "$GHOSTTY_COMMIT" ]; then
    git -C "$SRC" fetch origin "$GHOSTTY_COMMIT"
    git -C "$SRC" checkout -q "$GHOSTTY_COMMIT"
fi

STAMP="$OUT/.commit"
if [ -f "$STAMP" ] && [ "$(cat "$STAMP")" = "$GHOSTTY_COMMIT" ]; then
    echo "$OUT (cached)"
    exit 0
fi

rm -rf "$OUT"
TFLAG=""
[ "$TARGET" != "native" ] && TFLAG="-Dtarget=$TARGET"
(cd "$SRC" && zig build -Demit-lib-vt -Doptimize=ReleaseFast $TFLAG --prefix "$OUT/dist")

mkdir -p "$OUT/lib" "$OUT/pkgconfig"
cp -r "$OUT/dist/include" "$OUT/include"
# Windows targets emit ghostty-vt-static.lib, unix targets libghostty-vt.a.
if [ -f "$OUT/dist/lib/libghostty-vt.a" ]; then
    cp "$OUT/dist/lib/libghostty-vt.a" "$OUT/lib/libghostty-vt.a"
else
    cp "$OUT/dist/lib/ghostty-vt-static.lib" "$OUT/lib/libghostty-vt.a"
fi
cat > "$OUT/pkgconfig/libghostty-vt-static.pc" <<PC
prefix=$OUT
includedir=\${prefix}/include
libdir=\${prefix}/lib

Name: libghostty-vt-static
Description: Ghostty VT library (static, tuios pinned build)
Version: 0.1.0
Cflags: -I\${includedir}
Libs: -L\${libdir} -lghostty-vt
PC
echo "$GHOSTTY_COMMIT" > "$STAMP"
echo "$OUT"
