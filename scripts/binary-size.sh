#!/usr/bin/env bash
# Builds the tuios and tuios-slim binaries the way the release does
# (CGO_ENABLED=0, -trimpath, -ldflags "-s -w") for each target given, prints
# their sizes, and fails when one is larger than its budget. tuios-slim is
# cmd/tuios built with the slim tag; see docs/SLIM.md.
#
#   scripts/binary-size.sh                      # linux/amd64 and darwin/arm64
#   scripts/binary-size.sh linux/amd64          # one target
#
# The budgets are below. They sit a little above the size on the day they were
# set, so ordinary growth fits and a new dependency that pulls in a large tree
# does not. To raise one, measure the new size with this script on the Go
# version go.mod names, set the budget a little above it, and say in the
# commit what grew and why it is worth the bytes. See docs/perf.md, "Binary
# size budget".
set -euo pipefail

budget() {
	case "$1 $2" in
	"tuios linux/amd64") echo 28065000 ;;
	"tuios darwin/arm64") echo 26520000 ;;
	"tuios-slim linux/amd64") echo 17207000 ;;
	"tuios-slim darwin/arm64") echo 16380000 ;;
	*) echo "no budget for $1 on $2" >&2; return 1 ;;
	esac
}

# tags is the build tag each binary is built with.
tags() {
	case "$1" in
	tuios) echo "" ;;
	tuios-slim) echo slim ;;
	esac
}

cd "$(dirname "$0")/.."
targets=("$@")
if [ ${#targets[@]} -eq 0 ]; then
	targets=(linux/amd64 darwin/arm64)
fi

out=$(mktemp -d)
trap 'rm -rf "$out"' EXIT

go version
fail=0
for target in "${targets[@]}"; do
	for name in tuios tuios-slim; do
		limit=$(budget "$name" "$target")
		bin="$out/$name-${target%/*}-${target#*/}"
		CGO_ENABLED=0 GOOS="${target%/*}" GOARCH="${target#*/}" \
			go build -tags "$(tags "$name")" -trimpath -ldflags "-s -w" -o "$bin" ./cmd/tuios
		size=$(wc -c <"$bin" | tr -d ' ')
		room=$((limit - size))
		awk -v n="$name" -v t="$target" -v s="$size" -v l="$limit" -v r="$room" \
			'BEGIN { printf "%-10s %-14s %10d bytes (%.2f MiB)  budget %10d  room %9d\n", n, t, s, s / 1048576, l, r }'
		if [ "$size" -gt "$limit" ]; then
			echo "::error::$name for $target is $size bytes, over its budget of $limit by $((size - limit)). See docs/perf.md, \"Binary size budget\"."
			fail=1
		fi
	done
done
exit "$fail"
