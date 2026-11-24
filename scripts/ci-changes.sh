#!/usr/bin/env bash
# ci-changes.sh
#
# Decides which jobs of .github/workflows/ci.yml a change needs, and which Go
# packages each test job tests. It prints key=value lines, and appends them to
# $GITHUB_OUTPUT when that is set. The keys are listed under "Outputs" below.
#
# The change is BASE to HEAD:
#   - $CHANGES_BASE when set (a local run, or the replay of an old change);
#   - on a pull_request event, HEAD^1, because the checkout is the merge of
#     the head into the base;
#   - on a push, $CHANGES_BEFORE (github.event.before), but only for the six
#     jobs that a push to main filters by path: ghostty, learn_web,
#     clienttests, docker, nix and spec. Every other job runs in full on main;
#   - every other event runs everything, and so does CHANGES_FULL=true (a
#     pull request with the full-suite label).
#
# The rule is the same as e2e/tui/testselect: a path that no rule here names
# and that is in no Go package runs everything. So do a change to a go.mod,
# go.sum, go.work or go.work.sum beyond the pins of tuios.dev libraries, a
# change to CI files the pull request jobs run, and any error. The package
# sets come from scripts/go-test-affected.sh, once for each build the jobs
# test: linux, windows, darwin, js/wasm and the ghostty tag.
#
# Outputs (a list is space separated; "all" means every package):
#   pkgs          packages of the linux test jobs, "all", or "none"
#   ghostty_pkgs  packages to test on the ghostty backend, "all", or "none"
#   go            "true" when a Go file, an embedded file or a module file
#                 changed: format, tidy and the checks of the changed files
#   vet           "true" when one changed outside e2e/: vet, golangci-lint
#                 and govulncheck too. They do not read the e2e module.
#   lint_libs     library folders to lint, "all", or "none"
#   unit app_session isolation windows macos sketch_fma pins ghostty
#   learn_web clienttests docker nix binary_size mixed_versions spec
#                 "true" or "false", one per job. spec is the Lean model of
#                 the task cards (spec/tasks) and its differential test
#   lib_alone     JSON list of the "alone" library jobs: vt, sketch, nitpick, small
set -uo pipefail

ALL=(./... ./progstatus/... ./vt/... ./swatch/... ./shot/... ./tml/... ./plot/... ./loom/... ./sketch/... ./acp/... ./harness/... ./nitpick/...)
LIBS=(progstatus vt swatch shot tml plot loom sketch acp harness nitpick)
WINDOWS=(./internal/shimlink/ ./internal/herdrplugin/ ./internal/stopevent/ ./internal/herdrcli/ ./cmd/tuios-web/ ./internal/transcriptview/ ./internal/winacl/ ./internal/ptyspawn/ ./cmd/tuios/ ./internal/app/)
MACOS=(./internal/transcriptview/ ./internal/herdrplugin/ ./internal/session/)
WASM=(./cmd/tuios-wasm/... ./internal/webshell/ ./internal/learn/)
SELF=$(dirname "$0")

declare -A out=()
order=(pkgs ghostty_pkgs go vet lint_libs unit app_session isolation windows macos sketch_fma pins lib_alone ghostty learn_web clienttests docker nix binary_size mixed_versions spec)
emit() {
	local k
	for k in "${order[@]}"; do
		echo "$k=${out[$k]}"
		[ -z "${GITHUB_OUTPUT:-}" ] || echo "$k=${out[$k]}" >>"$GITHUB_OUTPUT"
	done
	exit 0
}
everything() {
	echo "ci-changes: everything runs: $1" >&2
	local k
	for k in "${order[@]}"; do out[$k]=true; done
	out[pkgs]=all out[ghostty_pkgs]=all out[lint_libs]=all out[lib_alone]='["vt","sketch","nitpick","small"]'
	emit
}

[ "${CHANGES_FULL:-}" != true ] || everything "the pull request has the full-suite label"

# The jobs a push to main runs only on a path match. spec also runs nightly.
pathgated=(ghostty learn_web clienttests docker nix spec)
push=false
base=${CHANGES_BASE:-}
if [ -z "$base" ]; then
	case ${GITHUB_EVENT_NAME:-} in
	pull_request)
		[ "$(git rev-list --parents -n 1 HEAD | wc -w)" -eq 3 ] || everything "the checkout is not a merge commit"
		base=HEAD^1
		;;
	push)
		push=true
		base=${CHANGES_BEFORE:-}
		if [ -z "$base" ] || [ -z "${base//0/}" ] || ! git cat-file -e "$base^{commit}" 2>/dev/null; then
			everything "a push whose previous commit is not in the checkout"
		fi
		;;
	*) everything "a ${GITHUB_EVENT_NAME:-local} run without CHANGES_BASE" ;;
	esac
fi

changed=$(git diff --no-renames --name-only "$base" HEAD) || everything "git diff failed"
if [ -z "$changed" ] && ! $push; then
	for k in "${order[@]}"; do out[$k]=false; done
	out[pkgs]=none out[ghostty_pkgs]=none out[lint_libs]=none out[lib_alone]='[]'
	emit
fi

# Flags the paths set on their own, before the package graph.
declare -A f=()
modfiles=false
has_go_pkg() { compgen -G "$1/*.go" >/dev/null; }
# in_pkg PATH: PATH sits in a folder, or under one, that holds Go files.
in_pkg() {
	local d
	d=$(dirname "$1")
	while [ "$d" != . ]; do
		has_go_pkg "$d" && return 0
		d=$(dirname "$d")
	done
	return 1
}
while IFS= read -r p; do
	# pkg: "must" when the path has to sit in a Go package, "may" when it
	# counts only if it does, "no" when the rule above said all it changes.
	pkg=must
	# The Lean model of the task cards, and the Go code it describes: the
	# card's move, its types and refusals, the automatic moves, and the
	# differential test.
	case $p in internal/session/task_move.go | internal/session/task.go | internal/session/task_auto.go | internal/session/task_move_spec_test.go) f[spec]=1 ;; esac
	case $p in
	# Files no job reads.
	.github/ISSUE_TEMPLATE/* | .github/PULL_REQUEST_TEMPLATE/* | .github/contributors-exclude | .github/dependabot.yml) pkg=no ;;
	# Workflows no job of ci.yml runs: e2e-tui.yml selects its own tests, and
	# the rest never run on a pull request.
	.github/workflows/e2e-tui.yml | .github/workflows/e2e-tui-map.yml | .github/workflows/e2e-kitty.yml | .github/workflows/fuzz.yml | .github/workflows/release.yml | .github/workflows/docker-publish.yml | .github/workflows/update-nix.yml | .github/workflows/contributors.yml | .github/workflows/star-history.yml | .github/workflows/issue-labeler.yml) pkg=no ;;
	.github/*) everything "$p changed" ;;
	scripts/ci-changes.sh | scripts/go-test-affected.sh | scripts/go-test-lanes.sh | scripts/go-test-lanes.durations | scripts/go-test-lanes-durations.jq | scripts/go-test-lanes-test.sh) everything "$p changed" ;;
	scripts/library-pins.sh) f[pins]=1 f[go]=1 pkg=no ;;
	scripts/binary-size.sh | scripts/binary-size-report.sh) f[binary_size]=1 pkg=no ;;
	scripts/check.sh | scripts/platforms.txt) f[go]=1 pkg=no ;;
	# The noinline list sets the GOFLAGS of every shipped or measured build of
	# cmd/tuios: binary size, the image, the mixed-version build and the
	# example tape build of go test ./.... The internal/lint tests check the
	# list and every build path that reads it.
	scripts/noinline-packages.txt | scripts/goflags.sh) f[binary_size]=1 f[docker]=1 f[mixed]=1 f[go]=1 f[lint_tests]=1 pkg=no ;;
	scripts/ghostty-lib.sh | scripts/minisign/*) f[ghostty]=1 pkg=no ;;
	scripts/e2e-mixed-versions.sh) f[mixed]=1 pkg=no ;;
	scripts/e2e-*.sh | scripts/fuzz-target.sh | scripts/contributors*.sh | scripts/contributors-mosaic.go | scripts/star-history.sh | scripts/rebase.sh | scripts/vt-rename.sh | scripts/install.sh | scripts/zigcc/*) pkg=no ;;
	scripts/*) everything "$p is a script no rule names" ;;
	Dockerfile | .dockerignore) f[docker]=1 pkg=no ;;
	flake.nix | flake.lock | tuios.nix | nix/*) f[nix]=1 pkg=no ;;
	.golangci.yml) f[go]=1 f[vet]=1 f[lint_all]=1 pkg=no ;;
	clienttests/*) f[clienttests]=1 pkg=no ;;
	spec/*) f[spec]=1 pkg=no ;;
	go.mod | */go.mod | go.sum | */go.sum | go.work | go.work.sum)
		# A change beyond the pins runs everything (go-test-affected.sh
		# prints "all"). Every module file is part of the Nix vendor hash and
		# of the image, and the pins check reads them.
		modfiles=true
		f[nix]=1 f[docker]=1 f[pins]=1 f[go]=1 f[vet]=1 f[clienttests]=1
		pkg=no
		;;
	# The end-to-end module: only its Go code is formatted, vetted and tidied
	# here. e2e-tui.yml selects its tests.
	# The mixed-version tests also run against the previous minor release.
	e2e/tui/multiclient_mixed_versions_test.go | e2e/tui/agent_subagent_list_test.go | e2e/tui/upgrade_confirm_test.go) f[go]=1 f[mixed]=1 pkg=no ;;
	e2e/*.go) f[go]=1 pkg=no ;;
	e2e/*) pkg=no ;;
	examples/*.tape) f[unit]=1 pkg=may ;;
	# Files no Go build reads. A test that reads one by a path up the tree
	# still runs: go-test-affected.sh finds it. In a Go package such a file
	# may be embedded, so it counts as the package.
	*.md | docs/* | assets/* | examples/* | integrations/* | LICENSE | CITATION.cff | FUNDING.yml | .markdownlint.json | .gitignore | .gitattributes | .envrc | justfile | install.sh | install-web.sh | .goreleaser.yml | demo.gif | demo.avif) pkg=may ;;
	esac
	case $pkg in
	no) continue ;;
	may) in_pkg "$p" || continue ;;
	must)
		# A Go file whose folder is gone was a package; go-test-affected.sh
		# runs everything for it.
		if ! in_pkg "$p" && ! { [ "${p%.go}" != "$p" ] && [ ! -d "$(dirname "$p")" ]; }; then
			everything "$p is in no Go package and no rule names it"
		fi
		;;
	esac
	f[go]=1 f[vet]=1
	top=${p%%/*}
	for l in "${LIBS[@]}"; do
		[ "$top" != "$l" ] || f[lib_$l]=1
	done
	[ "$top" != cmd ] || f[docker]=1
	case $p in cmd/tuios-web/* | internal/app/* | internal/input/* | internal/config/*) f[clienttests]=1 ;; esac
	[ "$top" != vt ] || f[ghostty]=1
	# Test files and test data never reach the binary.
	case $p in *_test.go | */testdata/*) ;; *) f[bin_src]=1 ;; esac
done <<<"$changed"
for l in "${LIBS[@]}"; do
	# tuios does not import nitpick, so a change there reaches neither the
	# image nor the clients.
	[ "$l" = nitpick ] && { [ -z "${f[lib_$l]:-}" ] || f[pins]=1; continue; }
	if [ -n "${f[lib_$l]:-}" ]; then f[docker]=1 f[clienttests]=1 f[pins]=1; fi
done

# A module file that changes more than a library pin runs everything. The
# check is go-test-affected.sh's; it prints "all" for it.
affected() { # NAME ENV... -- PATTERN...
	local name=$1 env=()
	shift
	while [ "$1" != -- ]; do env+=("$1"); shift; done
	shift
	env "${env[@]}" AFFECTED_BASE="$base" "$SELF/go-test-affected.sh" "$@" >"$tmp/$name" || echo fail >"$tmp/$name.err"
}
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
# Each build is one go list of its own, so they run side by side.
affected pkgs GOFLAGS= -- "${ALL[@]}" &
affected win GOOS=windows GOFLAGS= -- "${WINDOWS[@]}" &
affected mac GOOS=darwin GOFLAGS= -- "${MACOS[@]}" &
affected wasm GOOS=js GOARCH=wasm GOFLAGS= -- "${WASM[@]}" &
# A test that only reads a changed file, such as a doc, reads the same file
# on the ghostty backend, so that build leaves the readers out.
affected gpk GOFLAGS=-tags=ghostty AFFECTED_NO_READERS=1 -- "${ALL[@]}" &
affected bin GOFLAGS= -- ./cmd/tuios/ &
affected bindarwin GOOS=darwin GOFLAGS= -- ./cmd/tuios/ &
wait
for n in pkgs win mac wasm gpk bin bindarwin; do
	[ ! -e "$tmp/$n.err" ] || everything "go-test-affected.sh failed ($n)"
	grep -qx all "$tmp/$n" && everything "go-test-affected.sh selected every package ($n)"
done
pkgs=$(cat "$tmp/pkgs") win=$(cat "$tmp/win") mac=$(cat "$tmp/mac") wasm=$(cat "$tmp/wasm")
gpk=$(cat "$tmp/gpk") bin=$(cat "$tmp/bin") bindarwin=$(cat "$tmp/bindarwin")
# go-test-affected.sh finds a test that reads a changed file only by a "../"
# path in the same file. The internal/lint tests read from the repository
# root, so a rule above names them.
if [ -n "${f[lint_tests]:-}" ]; then
	pkgs=$(printf '%s\n%s\n' "$pkgs" github.com/Gaurav-Gosain/tuios/internal/lint | grep . | sort -u)
fi

# The packages whose tests link the emulator, as ghostty-vt.yml finds them.
vtlinked=$(go list -tags ghostty -test -f '{{.ForTest}} {{join .Deps " "}}' "${ALL[@]}" 2>/dev/null |
	awk '$1 != "" && / tuios.dev\/vt( |$)/ {print $1}' | sort -u) || everything "go list failed for ghostty"
vtlinked+=$'\n'$(go list ./vt/... 2>/dev/null)
gsel=$(comm -12 <(tr ' ' '\n' <<<"$gpk" | grep . | sort -u) <(grep . <<<"$vtlinked" | sort -u))

tf() { if [ -n "$1" ]; then echo true; else echo false; fi; }
list() { if [ -n "$1" ]; then tr '\n' ' ' <<<"$1" | sed 's/ *$//'; else echo none; fi; }

out[pkgs]=$(list "$pkgs")
out[ghostty_pkgs]=$(list "$gsel")
out[go]=$(tf "${f[go]:-}")
out[vet]=$(tf "${f[vet]:-}")
rest=$(grep -vE '/internal/(app|session)$' <<<"$pkgs")
out[unit]=$(tf "${f[go]:-}${f[unit]:-}$rest")
out[app_session]=$(tf "$(grep -E '/internal/(app|session)$' <<<"$pkgs")")
out[isolation]=$(tf "$(grep -E '^github.com/Gaurav-Gosain/tuios/(internal|cmd|pkg)/' <<<"$pkgs")")
out[windows]=$(tf "$win")
out[macos]=$(tf "$mac")
out[sketch_fma]=$(tf "$(grep -E '^tuios.dev/sketch(/|$)' <<<"$pkgs")")
out[ghostty]=$(tf "${f[ghostty]:-}$gsel")
out[learn_web]=$(tf "$wasm")
out[clienttests]=$(tf "${f[clienttests]:-}")
out[docker]=$(tf "${f[docker]:-}")
out[nix]=$(tf "${f[nix]:-}")
# The previous minor release talks to this build over the session package's
# wire, and the pane snapshot crosses it as vt/snapshot. The job runs when a
# changed Go file, not a test, is in a package the two build from.
wire=""
dirs=$(grep -E '\.go$' <<<"$changed" | grep -v '_test\.go$' | xargs -r -n1 dirname | sort -u | sed 's|^|./|')
if [ -n "$dirs" ]; then
	chpk=$(go list $dirs 2>/dev/null | sort -u)
	wdeps=$(go list -deps ./internal/session ./vt/snapshot 2>/dev/null | sort -u) || everything "go list failed for the wire packages"
	wire=$(comm -12 <(grep . <<<"$chpk") <(grep . <<<"$wdeps"))
fi
out[mixed_versions]=$(tf "${f[mixed]:-}$wire")
out[spec]=$(tf "${f[spec]:-}")
out[binary_size]=$(tf "${f[binary_size]:-}${f[bin_src]:+$bin$bindarwin}$($modfiles && echo y)")

# A library is linted, and tested alone, when one of its packages changed or
# its tests build a package that did. The lint of a library reads the others
# through go.work, so a change in plot lints loom too.
lint=() alone=()
for l in "${LIBS[@]}"; do
	if [ -n "${f[lint_all]:-}" ] || grep -qE "^tuios.dev/$l(/|$)" <<<"$pkgs"; then lint+=("$l"); fi
done
out[lint_libs]=$(list "${lint[*]:-}")
[ -z "${f[lint_all]:-}" ] || out[lint_libs]=all
[ -z "${f[lib_vt]:-}" ] || alone+=('"vt"')
[ -z "${f[lib_sketch]:-}" ] || alone+=('"sketch"')
[ -z "${f[lib_nitpick]:-}" ] || alone+=('"nitpick"')
for l in progstatus swatch shot tml plot loom acp harness; do
	if [ -n "${f[lib_$l]:-}" ]; then alone+=('"small"'); break; fi
done
if $modfiles; then alone=('"vt"' '"sketch"' '"nitpick"' '"small"'); fi
out[lib_alone]="[$(
	IFS=,
	echo "${alone[*]:-}"
)]"
out[pins]=$(tf "${f[pins]:-}")

# A push to main runs every job but the ones that filter by path there.
if $push; then
	for k in "${order[@]}"; do
		case " ${pathgated[*]} " in *" $k "*) continue ;; esac
		out[$k]=true
	done
	out[pkgs]=all out[lint_libs]=all out[lib_alone]='["vt","sketch","nitpick","small"]'
	[ "${out[ghostty]}" = false ] || out[ghostty_pkgs]=all
fi
emit
