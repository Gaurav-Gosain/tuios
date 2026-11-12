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

# Pinned because the library is a release artifact: the emulator must not
# drift under a tuios release without a deliberate bump here. libghostty-vt has
# its own releases (vt-X.Y.Z tags) with signed source tarballs, so the build
# uses those rather than a commit of the ghostty repository.
#
# Bump go.mitchellh.com/libghostty in vt/go.mod in the same change. The
# binding compiles against these headers and calls into this archive, so the
# two pins must move together. A binding built for older headers still
# compiles against newer ones, and the mismatch is silent.
#
# To bump: take the version and the two SHA-256 sums from the release's
# SHA256SUMS at https://release.files.ghostty.org/libghostty-vt/VERSION/, and
# ZIG_SERIES from minimum_zig_version in the tarball's build.zig.zon. The
# script checks both minisign signatures on every download, so a sum copied
# from a tampered SHA256SUMS fails the build.
GHOSTTY_VT_VERSION=1.0.1
GHOSTTY_VT_SHA256=333dfce97641ef9b536bb9e08907ad86abaaada50e8f7417b70d9284adef37aa
GHOSTTY_VT_DEPS_SHA256=91a4e10960a52bf5bf4b15aa2a2cddaff8a89d6cd903d3055bda4fd17de3356d

# The key that signs Ghostty and libghostty-vt releases, from
# PACKAGING-LIBGHOSTTY.md in the release tarball.
MINISIGN_KEY=RWQlAjJC23149WL2sEpT/l0QKy7hMIFhYdQOFy0Z7z7PbneUgvlsnYcV

RELEASE_URL=https://release.files.ghostty.org/libghostty-vt/$GHOSTTY_VT_VERSION

# The zig series this libghostty-vt release builds with. The build compares
# major and minor exactly and accepts any newer patch. A newer series fails
# mid-build on removed std APIs.
ZIG_SERIES=0.17

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
SRC="$CACHE/libghostty-vt-$GHOSTTY_VT_VERSION"
DEPS="$CACHE/libghostty-vt-$GHOSTTY_VT_VERSION-deps"
OUT="$CACHE/$TARGET"

# The CPU is stamped alongside the version so an archive left over from an
# older, native build is rebuilt rather than trusted.
STAMP="$OUT/.commit"
if [ -f "$STAMP" ] && [ "$(cat "$STAMP")" = "$GHOSTTY_VT_VERSION $CPU" ]; then
    echo "$OUT (cached)"
    exit 0
fi

# ZIG names the zig binary to use. It defaults to the first zig on PATH.
ZIG="${ZIG:-zig}"
export ZIG
command -v "$ZIG" >/dev/null || { echo "ghostty-lib.sh: zig not found. Zig $ZIG_SERIES.x is required. Set ZIG to a $ZIG_SERIES binary or put it on PATH." >&2; exit 1; }
zig_found=$("$ZIG" version 2>/dev/null) || zig_found=unknown
case "$zig_found" in
"$ZIG_SERIES".*) ;;
*)
    cat >&2 <<MSG
ghostty-lib.sh: Zig $ZIG_SERIES.x is required, but $ZIG reports $zig_found.
libghostty-vt $GHOSTTY_VT_VERSION does not build with other Zig versions.
Get Zig $ZIG_SERIES from https://ziglang.org/download or with anyzig or zvm.
Then put it first on PATH, or set ZIG to its path:
  ZIG=/path/to/zig-$ZIG_SERIES/zig $0 $TARGET
MSG
    exit 1
    ;;
esac

sha256() {
    if command -v sha256sum >/dev/null; then
        sha256sum "$1" | cut -d' ' -f1
    else
        shasum -a 256 "$1" | cut -d' ' -f1
    fi
}

# fetch NAME SHA256 downloads one release tarball and its signature into the
# cache, checks the pinned sum and the minisign signature, and unpacks it.
# A tarball that fails either check is deleted, so the next run downloads it
# again instead of trusting it.
fetch() {
    name=$1 want=$2
    file="$CACHE/dl/$name.tar.gz"
    mkdir -p "$CACHE/dl"
    if [ ! -f "$file" ]; then
        curl -fsSL --retry 3 -o "$file.part" "$RELEASE_URL/$name.tar.gz"
        mv "$file.part" "$file"
    fi
    curl -fsSL --retry 3 -o "$file.minisig" "$RELEASE_URL/$name.tar.gz.minisig"
    got=$(sha256 "$file")
    if [ "$got" != "$want" ]; then
        rm -f "$file" "$file.minisig"
        echo "ghostty-lib.sh: $name.tar.gz has SHA-256 $got, not the pinned $want." >&2
        exit 1
    fi
    if ! (cd "$ROOT" && go run ./scripts/minisign "$MINISIGN_KEY" "$file") >&2; then
        rm -f "$file" "$file.minisig"
        echo "ghostty-lib.sh: $name.tar.gz is not signed by the libghostty-vt release key." >&2
        exit 1
    fi
    rm -rf "${CACHE:?}/$name"
    tar -xzf "$file" -C "$CACHE"
}

[ -f "$SRC/LIB_VERSION" ] || fetch "libghostty-vt-$GHOSTTY_VT_VERSION" "$GHOSTTY_VT_SHA256"
[ -d "$DEPS" ] || fetch "libghostty-vt-$GHOSTTY_VT_VERSION-deps" "$GHOSTTY_VT_DEPS_SHA256"

rm -rf "$OUT"
TFLAG=""
[ "$TARGET" != "native" ] && TFLAG="-Dtarget=$TARGET"
# --system points zig at the unpacked dependency tarball, so the build fetches
# nothing. The global cache must start empty: with Zig 0.17 a cache that
# holds an archive of a package can crash the build with "reached unreachable
# code" (PACKAGING-LIBGHOSTTY.md). This overrides a ZIG_GLOBAL_CACHE_DIR the
# caller set, as CI does. The release tarball holds a LIB_VERSION file, which
# is what makes zig build build the library and not the Ghostty app, and the
# xcframework is off by default, so neither needs a flag.
ZIG_CACHE="$CACHE/zig-cache-$TARGET"
rm -rf "$ZIG_CACHE"
(cd "$SRC" && ZIG_GLOBAL_CACHE_DIR="$ZIG_CACHE" "$ZIG" build --system "$DEPS" -Doptimize=ReleaseFast -Dcpu="$CPU" $TFLAG --prefix "$OUT/dist")
rm -rf "$ZIG_CACHE"

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
Version: $GHOSTTY_VT_VERSION
Cflags: -I\${includedir}
Libs: -L\${libdir} -lghostty-vt
PC
echo "$GHOSTTY_VT_VERSION $CPU" > "$STAMP"
echo "$OUT"
