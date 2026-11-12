# Sourced, not run: sets up a go command to build against the libghostty-vt
# that scripts/ghostty-lib.sh built in $ghostty_native (its native folder),
# for the checkout at $ghostty_root.
#
#   ghostty_root=$ROOT ghostty_native=$cache/native; . "$ROOT/scripts/ghostty-env.sh"
#
# It exports PKG_CONFIG_PATH, and adds the folder and the pinned version to
# CGO_CFLAGS. The go command keys its build cache on CGO_CFLAGS but not on what
# pkg-config prints, and the cached cgo binding keeps the library folder it was
# first built against. Without this, a cache warmed against one libghostty-vt
# folder (another checkout's .ghostty-vt, another GHOSTTY_VT_CACHE) links that
# folder's library into a build pointed at another one, whatever its version.
# With the folder and the version in CGO_CFLAGS, each pairing gets its own
# cache entry.
#
# It also exports TUIOS_GHOSTTY_VT_PIN, the pinned version, which the vt tests
# (TestGhosttyLinksThePinnedLibrary in cmd/tuios) and install.sh hold the linked library to.

TUIOS_GHOSTTY_VT_PIN=$(sed -n 's/^GHOSTTY_VT_VERSION=//p' "$ghostty_root/scripts/ghostty-lib.sh")
ghostty_native=$(cd "$ghostty_native" && pwd)
PKG_CONFIG_PATH=$ghostty_native/pkgconfig
# Unset, CGO_CFLAGS is the go command's default (-O2 -g), which is kept.
CGO_CFLAGS="${CGO_CFLAGS:-$(go env CGO_CFLAGS)} -I$ghostty_native/include -DTUIOS_GHOSTTY_VT_VERSION=$TUIOS_GHOSTTY_VT_PIN"
export PKG_CONFIG_PATH CGO_CFLAGS TUIOS_GHOSTTY_VT_PIN
