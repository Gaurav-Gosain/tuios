#!/bin/sh
# Builds the guios app for one platform with MyGo's CLI, with a tuios of the
# same commit inside it for the daemon.
#
#   scripts/guios-app.sh [GOOS/GOARCH] [mygo build flags...]
#
# The default platform is this machine's. The app lands in
# cmd/guios/build/<os>-<arch>/: guios.app on macOS (signed ad hoc unless
# mygo.json names an identity), the executable and its packages on Linux.
# Signing and the disk image need macOS.
set -e
ROOT=$(cd "$(dirname "$0")/.." && pwd)
PLATFORM=${1:-$(go env GOOS)/$(go env GOARCH)}
[ $# -gt 0 ] && shift
OS=${PLATFORM%/*}
ARCH=${PLATFORM#*/}
VERSION=$(git -C "$ROOT" describe --tags --always --dirty 2>/dev/null || echo dev)
RES="$ROOT/cmd/guios/resources/$OS-$ARCH"
mkdir -p "$RES"
EXE=tuios
[ "$OS" = windows ] && EXE=tuios.exe
echo "building tuios $VERSION for $PLATFORM"
(cd "$ROOT" && CGO_ENABLED=0 GOOS=$OS GOARCH=$ARCH go build -trimpath \
	-ldflags "-s -w -X main.version=$VERSION" -o "$RES/$EXE" ./cmd/tuios)
MYGO=$(cd "$ROOT" && go list -m -f '{{.Version}}' github.com/egoist/mygo)
echo "building guios with mygo $MYGO"
cd "$ROOT/cmd/guios"
go run "github.com/egoist/mygo/cmd/mygo@$MYGO" build -platform "$PLATFORM" "$@"
