#!/bin/sh
# Build tuios from this checkout and install it on your PATH as `tuios` and
# `tuios-web`.
#
# Both are installed together and stamped with the same commit, because they are
# two halves of one thing: tuios-web is a client of the same daemon, and a
# tuios-web left behind by an upgrade is a client running last month's code
# against this month's daemon. That used to be silent. It is now visible - the
# two builds are compared at the handshake and the window says so - but the
# better answer is that it does not happen.
#
# Usage: scripts/install.sh [ghostty|pure] [options]
#
#   ghostty   link the libghostty-vt emulator (default). Needs zig; the pinned
#             static library is built on first use and cached in .ghostty-vt/.
#   pure      link the pure Go emulator. Needs nothing but go.
#
#   --prefix DIR    install into DIR (default: $TUIOS_PREFIX, else ~/.local/bin)
#   --kill-server   stop a running daemon after installing, without asking
#   --keep-server   leave a running daemon alone, without asking
#   -h, --help      show this text
#
# Both backends install under the same name, so switching is one run of this
# script with the other name. `tuios --version` reports which one is installed.
set -eu

usage() {
    awk 'NR==1 { next } /^#/ { sub(/^#[[:space:]]?/, ""); print; next } { exit }' "$0"
}

die() {
    printf 'install.sh: %s\n' "$*" >&2
    exit 1
}

say() {
    printf '%s\n' "$*"
}

note() {
    printf '\n%s\n' "$*" >&2
}

# Resolved from the script rather than the working directory, so the script
# runs from anywhere.
ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
[ -f "$ROOT/go.mod" ] || die "no tuios checkout above $0 (the script builds the tree it lives in)"

backend=ghostty
prefix=${TUIOS_PREFIX:-$HOME/.local/bin}
daemon_action=ask

while [ $# -gt 0 ]; do
    case $1 in
        ghostty | pure) backend=$1 ;;
        --prefix)
            [ $# -ge 2 ] || die "--prefix needs a directory"
            prefix=$2
            shift
            ;;
        --prefix=*) prefix=${1#--prefix=} ;;
        --kill-server) daemon_action=stop ;;
        --keep-server) daemon_action=keep ;;
        -h | --help)
            usage
            exit 0
            ;;
        *) die "unknown argument: $1 (try --help)" ;;
    esac
    shift
done

command -v go >/dev/null || die "go not found. Install Go from https://go.dev/dl and re-run."

# The same socket the daemon picks; see internal/session/manager_unix.go.
if [ -n "${XDG_RUNTIME_DIR:-}" ]; then
    socket=$XDG_RUNTIME_DIR/tuios/tuios.sock
else
    socket=/tmp/tuios-$(id -u)/tuios.sock
fi

# Prints the pid of a live daemon, or fails. The pid file outlives a crash, so
# the signal probe is what makes the answer true.
daemon_pid() {
    [ -f "$socket.pid" ] || return 1
    _pid=$(cat "$socket.pid" 2>/dev/null) || return 1
    [ -n "$_pid" ] || return 1
    kill -0 "$_pid" 2>/dev/null || return 1
    printf '%s\n' "$_pid"
}

buildtags=''
if [ "$backend" = ghostty ]; then
    command -v zig >/dev/null || die "zig not found, and the ghostty backend needs it to build libghostty-vt.
Install zig >= 0.16 from https://ziglang.org/download, or build the pure Go
emulator instead with: $0 pure"

    cache=${GHOSTTY_VT_CACHE:-$ROOT/.ghostty-vt}
    if [ -d "$cache/native" ]; then
        say "==> libghostty-vt"
    else
        say "==> libghostty-vt (first run clones ghostty and takes a few minutes)"
    fi
    "$ROOT/scripts/ghostty-lib.sh" native

    PKG_CONFIG_PATH=$cache/native/pkgconfig
    [ -d "$PKG_CONFIG_PATH" ] || die "no pkgconfig directory at $PKG_CONFIG_PATH"
    export PKG_CONFIG_PATH
    export CGO_ENABLED=1
    buildtags='-tags=ghostty'
fi

mkdir -p "$prefix" || die "cannot create $prefix"
# Checked up front so a system prefix fails here rather than after the build.
[ -w "$prefix" ] || die "$prefix is not writable. Pick a prefix you own, or re-run under sudo."
dest=$prefix/tuios
webdest=$prefix/tuios-web

# The commit is stamped in rather than left to the stamps the go command
# embeds by itself: in a linked worktree those come back naming the main
# checkout's HEAD, so a build made on a branch would report the wrong commit.
#
# It is stamped into the version string as well, and both binaries get the same
# one. The version is what a client announces to the daemon at the handshake, so
# "dev" on its own says nothing: two builds months apart both announce "dev" and
# a mismatch that matters reads as a match. With the commit in it the daemon and
# the client can tell whether they are the same build, and say so when they are
# not.
ldflags='-s -w -X main.builtBy=install.sh'
buildversion=dev
if rev=$(cd "$ROOT" && git rev-parse HEAD 2>/dev/null); then
    buildversion="dev+$(printf '%s' "$rev" | cut -c1-12)"
    # A tree with uncommitted work is not the commit it sits on, and two builds
    # from it are not each other either. Saying so is what keeps the version
    # honest for the comparison the daemon makes at the handshake.
    if [ -n "$(cd "$ROOT" && git status --porcelain 2>/dev/null)" ]; then
        rev="$rev-dirty"
        buildversion="$buildversion-dirty"
    fi
    ldflags="$ldflags -X main.commit=$rev -X main.date=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
fi
ldflags="$ldflags -X main.version=$buildversion"

# Built next to the destination so each install is one rename on one filesystem:
# writing over the destination in place would fail with ETXTBSY while a daemon
# is running it.
tmp=$prefix/.tuios.install.$$
webtmp=$prefix/.tuios-web.install.$$
trap 'rm -f "$tmp" "$webtmp"' EXIT HUP INT TERM

say "==> tuios ($backend backend)"
(cd "$ROOT" && go build ${buildtags:+"$buildtags"} -trimpath \
    -ldflags "$ldflags" -o "$tmp" ./cmd/tuios)
chmod 755 "$tmp"

# Built before either is moved into place, so a compile error in one does not
# leave the pair half installed and mismatched - the state this is here to
# prevent.
say "==> tuios-web ($backend backend)"
(cd "$ROOT" && go build ${buildtags:+"$buildtags"} -trimpath \
    -ldflags "$ldflags" -o "$webtmp" ./cmd/tuios-web)
chmod 755 "$webtmp"

mv -f "$tmp" "$dest"
mv -f "$webtmp" "$webdest"
trap - EXIT HUP INT TERM

say ""
say "installed $dest"
say "installed $webdest"
"$dest" --version

case ":$PATH:" in
    *":$prefix:"*)
        found=$(command -v tuios 2>/dev/null || true)
        if [ -n "$found" ] && [ "$found" != "$dest" ]; then
            note "PATH still finds tuios at $found, which shadows what was just installed.
Delete that one, or install over it with:
    $0 $backend --prefix $(dirname "$found")"
        fi
        ;;
    *)
        note "$prefix is not on your PATH, so \`tuios\` will not be found. Add it:
    fish:      fish_add_path $prefix
    bash/zsh:  export PATH=\"$prefix:\$PATH\""
        ;;
esac

# A running daemon keeps serving the binary it started from, so until it is
# restarted the new build is only what new clients run.
if pid=$(daemon_pid); then
    case $daemon_action in
        keep) daemon_action=no ;;
        ask)
            if [ -t 0 ] && [ -r /dev/tty ]; then
                printf '\nA daemon (pid %s) is still running the previous build. Stop it now? [y/N] ' "$pid" >&2
                read -r answer </dev/tty || answer=n
                case $answer in
                    [yY] | [yY][eE][sS]) daemon_action=stop ;;
                    *) daemon_action=no ;;
                esac
            else
                daemon_action=no
            fi
            ;;
    esac

    if [ "$daemon_action" = stop ]; then
        say "==> stopping the daemon"
        "$dest" kill-server
    else
        note "A daemon (pid $pid) is still running the previous build, and every
attached session goes through it. Run this when you are ready to switch:
    tuios kill-server
Sessions are saved on the way out and restored when the daemon starts again."
    fi
fi
