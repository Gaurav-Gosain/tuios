#!/usr/bin/env bash
# e2e-mixed-versions.sh NEW OLD [GROUP]
#
# Runs the end-to-end tests that pair this build with the previous minor
# release. The support window is one minor release: a client and a daemon
# one minor apart work together. NEW is the binary under test, OLD a tuios of
# the previous minor, such as the v0.12.1 release.
#
#   - TestSubagentListFromHooks and TestPaneUpgradeRunsNoFileWithoutADialog
#     attach an OLD client beside NEW ones (TUIOS_E2E_OLD_BIN).
#   - TestForwardToAnOlderHost runs tuios forward against an OLD far machine
#     (TUIOS_E2E_OLD_BIN), whose link proxy cannot read the newer frames.
#   - TestMixedVersionsReshapeOneTree runs twice: an OLD daemon with NEW
#     clients, and a NEW daemon with one OLD client and one NEW one.
#
# GROUP is old-client or tree for one of the two, and both by default.
# Run it from the repository root. The logs go to $MIXED_LOGS (default
# .dev/mixed-versions).
set -euo pipefail
new=$(realpath "$1")
old=$(realpath "$2")
group=${3:-all}
case $group in all | old-client | tree) ;; *)
	echo "unknown group $group: use old-client or tree" >&2
	exit 2
	;;
esac
logs=$(realpath -m "${MIXED_LOGS:-.dev/mixed-versions}")
mkdir -p "$logs"
echo "mixed versions: new $("$new" --version | head -1), old $("$old" --version | head -1)"
cd e2e/tui
export TUIOS_E2E=1 TUIOS_E2E_BIN=$new
status=0
run() { # NAME PATTERN ENV...
	local name=$1 pattern=$2
	shift 2
	if env "$@" go test -count=1 -timeout 15m -v -run "$pattern" . >"$logs/$name.log" 2>&1; then
		echo "pass: $name"
	else
		echo "FAIL: $name (log: $logs/$name.log)"
		grep -E '^\s*--- FAIL|^\s+\S+_test\.go:[0-9]+:' "$logs/$name.log" | head -40 || true
		status=1
	fi
}
if [ "$group" != tree ]; then
	run old-client '^(TestSubagentListFromHooks|TestPaneUpgradeRunsNoFileWithoutADialog)$' TUIOS_E2E_OLD_BIN="$old"
	run old-host '^TestForwardToAnOlderHost$' TUIOS_E2E_OLD_BIN="$old"
fi
if [ "$group" != old-client ]; then
	run old-daemon '^TestMixedVersionsReshapeOneTree$' TUIOS_E2E_MIX_DAEMON="$old" TUIOS_E2E_MIX_A="$new" TUIOS_E2E_MIX_B="$new"
	run old-and-new-client '^TestMixedVersionsReshapeOneTree$' TUIOS_E2E_MIX_DAEMON="$new" TUIOS_E2E_MIX_A="$old" TUIOS_E2E_MIX_B="$new"
fi
exit $status
