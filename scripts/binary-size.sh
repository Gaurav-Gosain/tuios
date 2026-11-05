#!/usr/bin/env bash
# Builds the tuios binary the way the release does (CGO_ENABLED=0, -trimpath,
# -ldflags "-s -w") for each target given, prints its size, and fails when one
# is larger than the ceiling.
#
#   scripts/binary-size.sh                      # linux/amd64 and darwin/arm64
#   scripts/binary-size.sh linux/amd64          # one target
#
# There is one ceiling for every target, set in docs/perf.md, "Binary size
# budget". A pull request no longer edits a budget here. It states its size
# cost in its body, and CI posts the size against main on the pull request.
#
# With SIZE_OUT set, the script also writes one "target bytes" line per target
# to that file, for the CI job that compares against main.
set -euo pipefail

# 50 MB. Raise it only on purpose, in its own commit, with docs/perf.md.
ceiling=50000000

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
	bin="$out/tuios-${target%/*}-${target#*/}"
	CGO_ENABLED=0 GOOS="${target%/*}" GOARCH="${target#*/}" \
		go build -trimpath -ldflags "-s -w" -o "$bin" ./cmd/tuios
	size=$(wc -c <"$bin" | tr -d ' ')
	room=$((ceiling - size))
	awk -v t="$target" -v s="$size" -v l="$ceiling" -v r="$room" \
		'BEGIN { printf "%-14s %10d bytes (%.2f MiB)  ceiling %10d  room %9d\n", t, s, s / 1048576, l, r }'
	if [ -n "${SIZE_OUT:-}" ]; then
		echo "$target $size" >>"$SIZE_OUT"
	fi
	if [ "$size" -gt "$ceiling" ]; then
		echo "::error::tuios for $target is $size bytes, over the $ceiling byte ceiling by $((size - ceiling)). See docs/perf.md, \"Binary size budget\"."
		fail=1
	fi
done
exit "$fail"
