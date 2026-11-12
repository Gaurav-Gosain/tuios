#!/bin/sh
# Prints the GOFLAGS for a build of cmd/tuios as it ships: the GOFLAGS go
# already has (the environment or go env -w), then -gcflags=PATTERN=-l for
# each package in scripts/noinline-packages.txt. Use it as
#
#   GOFLAGS="$(scripts/goflags.sh)" go build ./cmd/tuios
#
# Set GOFLAGS on the go build command only: in GOFLAGS the -gcflags also
# reach go test and go vet.
set -eu
list="$(cd "$(dirname "$0")" && pwd)/noinline-packages.txt"
flags=$(go env GOFLAGS)
while IFS= read -r line || [ -n "$line" ]; do
	line=${line%%#*}
	set -- $line
	[ $# -gt 0 ] || continue
	flags="$flags -gcflags=$1=-l"
done <"$list"
printf '%s\n' "${flags# }"
