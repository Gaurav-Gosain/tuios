#!/usr/bin/env bash
# collect.sh BIN OUT [JOBS]
#
# Runs each end-to-end test named on stdin in its own process against BIN, a
# tuios built with -cover -tags coverflush, and writes OUT/<Test>.files: the
# source files whose code the test ran. Code that runs in every process (package
# init and the start of main) is taken out first, from a run of
# `BIN --version`, so a package counts only when the test reached code of its
# own. testselect -build turns the .files files into the test map.
#
# A test that fails is run once more. Its coverage is kept either way, and its
# name goes to OUT/failed.txt, because a test that stopped early may have
# missed code it would reach on a pass. testselect marks those tests to run on
# every change.
#
# Run from e2e/tui with TUIOS_E2E set.
set -euo pipefail

bin=$(realpath "$1")
out=$(realpath -m "$2")
jobs=${3:-1}
mkdir -p "$out"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

go test -c -o "$work/e2e.test" .

mkdir -p "$work/base"
GOCOVERDIR="$work/base" "$bin" --version >/dev/null
go tool covdata textfmt -i "$work/base" -o "$work/base.txt"
awk 'NR > 1 && $NF > 0 { print $1 }' "$work/base.txt" >"$work/base.keys"
# The functions that every process runs. A change inside one of them reaches
# every test that uses what it sets up, so testselect treats it like a change
# to a declaration. Lines are "path/file.go:line: name percent".
(cd ../.. && go tool cover -func="$work/base.txt") |
	awk '$NF != "0.0%" && $1 != "total:" { sub(/:[0-9]+:$/, "", $1); print $1, $2 }' | sort -u >"$out/init.funcs"

one() {
	local name=$1 dir="$work/cov/$1" status=0
	for attempt in 1 2; do
		rm -rf "$dir"
		mkdir -p "$dir"
		status=0
		GOCOVERDIR="$dir" TUIOS_E2E_BIN="$bin" timeout 15m "$work/e2e.test" \
			-test.count=1 -test.timeout 14m -test.run "^${name}\$" >"$dir.log" 2>&1 || status=$?
		[ "$status" -eq 0 ] && break
	done
	if [ "$status" -ne 0 ]; then
		echo "$name" >>"$out/failed.txt"
	fi
	if ls "$dir"/covmeta.* >/dev/null 2>&1; then
		go tool covdata textfmt -i "$dir" -o "$dir.txt" 2>/dev/null || true
	fi
	if [ -s "$dir.txt" ]; then
		# A profile line is "path/file.go:start,end statements count".
		awk 'NR == FNR { base[$1] = 1; next }
			FNR > 1 && $NF > 0 && !($1 in base) { sub(/:.*/, "", $1); print $1 }' \
			"$work/base.keys" "$dir.txt" | sort -u >"$out/$name.files"
	else
		: >"$out/$name.files"
	fi
	rm -rf "$dir" "$dir.txt" "$dir.log"
	echo "$name $status $(wc -l <"$out/$name.files")"
}
export -f one
export work out bin
xargs -P "$jobs" -I{} bash -c 'one {}'
