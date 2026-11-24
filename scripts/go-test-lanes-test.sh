#!/usr/bin/env bash
# go-test-lanes-test.sh
#
# Checks which go test processes scripts/go-test-lanes.sh starts for each
# selection of packages. A fake go on PATH answers go list and go test -list
# and records each go test run, so the check needs no build and no network.
# The split packages are example.test/app and example.test/session.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
mkdir "$work/bin"
cat >"$work/bin/go" <<'FAKE'
#!/usr/bin/env bash
case $1 in
list)
	shift
	for a in "$@"; do
		case $a in
		./...) printf '%s\n' example.test/app example.test/session example.test/other example.test/more ;;
		./nope) echo "package nope: no such package" >&2; exit 1 ;;
		./*) echo "example.test/${a#./}" ;;
		*) echo "$a" ;;
		esac
	done
	;;
test)
	shift
	if [ "$1" = -list ]; then
		printf 'TestA\nTestB\nTestC\nTestD\nok\n'
		exit 0
	fi
	echo "$*" >>"$FAKE_GO_RUNS"
	;;
esac
FAKE
chmod +x "$work/bin/go"

fail=0
# check NAME EXPECTED_RUNS ENV... -- PATTERN...
# EXPECTED_RUNS is the number of go test processes, or "error".
check() {
	local name=$1 want=$2
	shift 2
	local envs=()
	while [ "$1" != -- ]; do
		envs+=("$1")
		shift
	done
	shift
	: >"$work/runs"
	local rc=0
	PATH="$work/bin:$PATH" FAKE_GO_RUNS="$work/runs" LOG="$work/log" LANES=2 \
		env "${envs[@]}" "$here/go-test-lanes.sh" -count=1 -- "$@" >/dev/null 2>"$work/err" || rc=$?
	local got
	got=$(wc -l <"$work/runs")
	if [ "$want" = error ]; then
		[ "$rc" -ne 0 ] || { echo "FAIL $name: expected an error, exit 0"; fail=1; return; }
	else
		[ "$rc" -eq 0 ] || { echo "FAIL $name: exit $rc: $(cat "$work/err")"; fail=1; return; }
		[ "$got" -eq "$want" ] || { echo "FAIL $name: $got go test runs, want $want"; fail=1; return; }
	fi
	echo "ok   $name"
}

split='SPLIT=./app ./session'
# Two lanes for each picked split package, one more for the rest.
check "app only" 2 "$split" -- ./app
check "session only" 2 "$split" -- ./session
check "both" 4 "$split" -- ./app ./session
check "neither (rest only)" 1 "$split" -- ./other
check "all" 5 "$split" -- ./...
check "app and rest" 3 "$split" -- ./app ./other
check "app only, ONLY=rest" 0 "$split" ONLY=rest -- ./app
check "app and rest, ONLY=split" 2 "$split" ONLY=split -- ./app ./other
check "session only, ONLY=split" 2 "$split" ONLY=split -- ./session
check "other only, ONLY=split" 0 "$split" ONLY=split -- ./other
check "a SPLIT name that is no package" error 'SPLIT=./app ./nope' -- ./app

# lanes NAME WANT ENV...: the -run pattern of each lane of example.test/app,
# one per line, must equal WANT.
lanes() {
	local name=$1 want=$2
	shift 2
	: >"$work/runs"
	if ! PATH="$work/bin:$PATH" FAKE_GO_RUNS="$work/runs" LOG="$work/log" LANES=2 SPLIT=./app \
		env "$@" "$here/go-test-lanes.sh" -count=1 -- ./app >/dev/null 2>"$work/err"; then
		echo "FAIL $name: $(cat "$work/err")"
		fail=1
		return
	fi
	local got
	got=$(grep -o -- '-run [^ ]*' "$work/runs" | sort)
	if [ "$got" != "$want" ]; then
		printf 'FAIL %s: lanes\n%s\nwant\n%s\n' "$name" "$got" "$want"
		fail=1
		return
	fi
	echo "ok   $name"
}

printf 'example.test/app TestA 9\nexample.test/app TestB 1\nexample.test/app TestC 1\n# a comment\nexample.test/other TestD 50\n' >"$work/durations"
# Without times the four tests split two and two.
lanes "lanes by count" "$(printf -- '-run ^(TestA|TestB)$\n-run ^(TestC|TestD)$')" DURATIONS="$work/none"
# TestA takes most of the time, so it runs alone. TestD has no time for this
# package and counts at the mean of the others.
lanes "lanes by time" "$(printf -- '-run ^(TestA)$\n-run ^(TestB|TestC|TestD)$')" DURATIONS="$work/durations"

exit $fail
