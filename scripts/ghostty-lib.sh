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

# The archive outlives the machine that built it: CI restores it from a cache
# shared by heterogeneous runners, and release binaries link it and then run on
# whatever a user has. A native build bakes in the builder's CPU, so an archive
# built where AVX-512 exists dies with SIGILL where it does not. Baseline is
# the only portable answer. Cross targets already resolve to baseline; saying
# it once covers native too.
CPU=baseline

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

# The CPU is stamped alongside the commit so an archive left over from an
# older, native build is rebuilt rather than trusted.
STAMP="$OUT/.commit"
if [ -f "$STAMP" ] && [ "$(cat "$STAMP")" = "$GHOSTTY_COMMIT $CPU" ]; then
    echo "$OUT (cached)"
    exit 0
fi

rm -rf "$OUT"
TFLAG=""
[ "$TARGET" != "native" ] && TFLAG="-Dtarget=$TARGET"
# We link the static archive, never the xcframework. Ghostty defaults
# emit-xcframework on when an xcodebuild is merely on PATH, which the Command
# Line Tools install a stub for -- it then fails the build on any mac without
# full Xcode. Saying no also skips the universal build the packaging needs.
(cd "$SRC" && zig build -Demit-lib-vt -Demit-xcframework=false -Doptimize=ReleaseFast -Dcpu="$CPU" $TFLAG --prefix "$OUT/dist")

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
echo "$GHOSTTY_COMMIT $CPU" > "$STAMP"
echo "$OUT"
