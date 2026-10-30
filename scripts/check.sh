#!/usr/bin/env bash
# The deterministic Go checks, in one gate. `just check` runs it.
#
#   scripts/check.sh                 # every module, every package
#   scripts/check.sh --changed       # only what this branch changed against
#                                    # origin/main (the form to run before a
#                                    # pull request is marked ready)
#   scripts/check.sh --changed REF   # the same against another base
#   scripts/check.sh --only vet,lint # a subset, by check name
#
# Checks, in order (a check marked "report" prints its findings and never
# fails the gate):
#
#   fmt        gofmt -l and goimports -l
#   vet        go vet, plus -tags ghostty when libghostty-vt is built
#   lint       golangci-lint with .golangci.yml (staticcheck, errcheck, govet,
#              ineffassign, unused, gosec); the CI version unless
#              GOLANGCI_LINT names another command
#   tuioslint  the internal/lint tests (colour literals, child processes, quits)
#   vuln       govulncheck, for each module in the scope
#   fix        the go fix modernizers, one line per finding (report);
#              go fix -diff ./... prints them as patches
#   gopls      gopls check on the Go files the branch changed
#   tidy       go mod tidy -diff for every module, and library-pins.sh check
#   cross      go build for linux, darwin, windows and freebsd
#   deadcode   unreachable functions (report)
#
# Each check writes its full output under $CHECK_LOG_DIR (default
# .dev/check), and the gate ends with one line per check.
set -uo pipefail

cd "$(dirname "$0")/.."
root=$PWD

modules=(. e2e/tui progstatus vt swatch shot tml plot loom sketch)
cross_goos=(linux darwin windows freebsd)

changed=0
base=origin/main
only=""
while [ $# -gt 0 ]; do
	case "$1" in
	--changed)
		changed=1
		if [ $# -gt 1 ] && [ "${2#-}" = "$2" ]; then
			base=$2
			shift
		fi
		;;
	--only)
		only=${2:-}
		shift
		;;
	-h | --help)
		sed -n '2,/^set -uo/p' "$0" | sed '$d; s/^# \{0,1\}//'
		exit 0
		;;
	*)
		echo "check: unknown argument $1. Run scripts/check.sh --help." >&2
		exit 2
		;;
	esac
	shift
done

log_dir=${CHECK_LOG_DIR:-$root/.dev/check}
mkdir -p "$log_dir"
rm -f "$log_dir"/*.log

# The tools run through `go run` at a pinned version, so they are built with
# the local Go. A binary on PATH built with an older Go than go.mod asks for
# fails to type-check the tree, or misses newer syntax. An environment
# variable of the same name overrides each one. golangci-lint follows the
# version in the lint workflow.
lint_version=$(sed -n 's/^ *version: \(v[0-9.]*\)$/\1/p' .github/workflows/lint.yml | head -1)
GOLANGCI_LINT=${GOLANGCI_LINT:-go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@${lint_version:-v2.14.0}}
GOVULNCHECK=${GOVULNCHECK:-go run golang.org/x/vuln/cmd/govulncheck@v1.9.0}
GOIMPORTS=${GOIMPORTS:-go run golang.org/x/tools/cmd/goimports@v0.52.0}
GOPLS=${GOPLS:-go run golang.org/x/tools/gopls@v0.24.0}
DEADCODE=${DEADCODE:-go run golang.org/x/tools/cmd/deadcode@v0.52.0}

# libghostty-vt, for the -tags ghostty build. A worktree finds the one the
# main checkout built.
ghostty_pc=""
for dir in "$root/.ghostty-vt/native/pkgconfig" "$(git rev-parse --path-format=absolute --git-common-dir 2>/dev/null)/../.ghostty-vt/native/pkgconfig"; do
	if [ -f "$dir/libghostty-vt-static.pc" ]; then
		ghostty_pc=$(cd "$dir" && pwd)
		break
	fi
done

# ---------------------------------------------------------------- scope

# module_of prints the module folder that holds path.
module_of() {
	local path=$1 best=. mod
	for mod in "${modules[@]}"; do
		[ "$mod" = . ] && continue
		case "$path" in "$mod"/*) [ ${#mod} -gt ${#best} ] && best=$mod ;; esac
	done
	printf '%s\n' "$best"
}

changed_files=()
if [ "$changed" = 1 ]; then
	if ! merge_base=$(git merge-base "$base" HEAD 2>/dev/null); then
		echo "check: no merge base with $base. Run git fetch origin, or name a base." >&2
		exit 2
	fi
	# Committed changes, staged and unstaged changes, and new files.
	mapfile -t changed_files < <(
		{
			git diff --name-only --diff-filter=d "$merge_base"
			git ls-files --others --exclude-standard
		} | sort -u
	)
fi

changed_go=()
for f in "${changed_files[@]}"; do
	case "$f" in *.go) [ -f "$f" ] && changed_go+=("$f") ;; esac
done

# pkgs MODULE prints the package patterns to check in MODULE, relative to it,
# or nothing when the scope leaves the module out.
pkgs() {
	local mod=$1 f dir
	if [ "$changed" = 0 ]; then
		echo ./...
		return
	fi
	for f in "${changed_go[@]}"; do
		[ "$(module_of "$f")" = "$mod" ] || continue
		dir=$(dirname "$f")
		[ "$mod" != . ] && dir=${dir#"$mod"}
		dir=${dir#/}
		# A package outside the build (testdata, a folder of fixtures) is not one.
		case "/$dir/" in */testdata/*) continue ;; esac
		echo "./${dir:-.}"
	done | sort -u
}

# module_touched MODULE reports whether the scope reaches MODULE's go.mod.
module_touched() {
	local mod=$1 f
	[ "$changed" = 0 ] && return 0
	for f in "${changed_files[@]}"; do
		[ "$(module_of "$f")" = "$mod" ] || continue
		case "$f" in *.go | */go.mod | */go.sum | go.mod | go.sum) return 0 ;; esac
	done
	return 1
}

# go_files prints the tracked and new Go files in the scope.
go_files() {
	if [ "$changed" = 1 ]; then
		[ ${#changed_go[@]} -gt 0 ] && printf '%s\n' "${changed_go[@]}"
	else
		git ls-files '*.go'
		git ls-files --others --exclude-standard '*.go'
	fi
}

# ---------------------------------------------------------------- runner

names=()
results=()
counts=()
times=()

want() {
	[ -z "$only" ] && return 0
	case ",$only," in *",$1,"*) return 0 ;; esac
	return 1
}

# run NAME MODE FUNCTION: MODE is gate or report. FUNCTION writes its findings
# to stdout, one per line, and returns non-zero when it found any. It returns
# 3 when its tool is missing.
run() {
	local name=$1 mode=$2 fn=$3 log="$log_dir/$1.log" start status n
	want "$name" || return 0
	printf '== %s\n' "$name"
	start=$(date +%s)
	"$fn" >"$log" 2>&1
	status=$?
	n=$(grep -c . "$log" || true)
	if [ "$status" = 3 ]; then
		results+=(skip)
		n=0
	elif [ "$status" = 0 ]; then
		results+=(pass)
		n=0
	elif [ "$mode" = report ]; then
		results+=(report)
	else
		results+=(FAIL)
		tail -n 40 "$log"
	fi
	names+=("$name")
	counts+=("$n")
	times+=("$(($(date +%s) - start))s")
}

# buildable keeps the package patterns that hold a non-test Go file. go build
# refuses a folder of tests alone when it is named on its own.
buildable() {
	local mod=$1 p
	while IFS= read -r p; do
		if [ "$p" = ./... ]; then
			echo "$p"
		elif find "$mod/${p#./}" -maxdepth 1 -name '*.go' ! -name '*_test.go' | grep -q .; then
			echo "$p"
		fi
	done
}

# in_modules CMD...: runs CMD in each module the scope reaches, with that
# module's package patterns appended. Fails when any run fails.
in_modules() {
	local mod rc=0 p
	for mod in "${modules[@]}"; do
		mapfile -t p < <(pkgs "$mod")
		[ ${#p[@]} -eq 0 ] && continue
		(cd "$mod" && "$@" "${p[@]}") || rc=1
	done
	return $rc
}

# in_build_modules CMD...: in_modules, for the packages go build accepts.
in_build_modules() {
	local mod rc=0 p
	for mod in "${modules[@]}"; do
		mapfile -t p < <(pkgs "$mod" | buildable "$mod")
		[ ${#p[@]} -eq 0 ] && continue
		(cd "$mod" && "$@" "${p[@]}") || rc=1
	done
	return $rc
}

# ---------------------------------------------------------------- checks

check_fmt() {
	local files out rc=0
	mapfile -t files < <(go_files)
	[ ${#files[@]} -eq 0 ] && return 0
	out=$(gofmt -l "${files[@]}")
	[ -n "$out" ] && { echo "$out" | sed 's/$/: gofmt/'; rc=1; }
	out=$($GOIMPORTS -l "${files[@]}") || rc=1
	[ -n "$out" ] && { echo "$out" | sed 's/$/: goimports/'; rc=1; }
	return $rc
}

check_vet() {
	local rc=0
	in_modules go vet || rc=1
	if [ -n "$ghostty_pc" ]; then
		PKG_CONFIG_PATH=$ghostty_pc in_modules go vet -tags ghostty || rc=1
	fi
	return $rc
}

check_lint() {
	# The whole of each changed package, not only the changed lines: a change
	# to one function can raise a finding at its callers.
	local mod rc=0 p args=(run --timeout 20m)
	for mod in "${modules[@]}"; do
		# e2e/tui is not in the lint workflow, and its tests are the suite's
		# own harness. vet and the build still cover it.
		[ "$mod" = e2e/tui ] && continue
		mapfile -t p < <(pkgs "$mod")
		[ ${#p[@]} -eq 0 ] && continue
		(cd "$mod" && $GOLANGCI_LINT "${args[@]}" "${p[@]}") || rc=1
	done
	return $rc
}

check_tuioslint() {
	[ "$changed" = 1 ] && [ ${#changed_go[@]} -eq 0 ] && return 0
	go test -count=1 ./internal/lint/
}

check_vuln() {
	local mod rc=0
	for mod in "${modules[@]}"; do
		module_touched "$mod" || continue
		(cd "$mod" && $GOVULNCHECK ./...) || rc=1
	done
	return $rc
}

# The fixers go fix applies, run as a vet tool, so each finding is one line
# instead of a patch. `go fix -diff ./...` in a module prints the patches.
check_fix() {
	in_modules go vet -vettool="$(go env GOTOOLDIR)/fix"
}

# gopls also type-checks each file under other platforms (js/wasm, windows,
# freebsd/386) and tags those diagnostics with the platform. Many of them are
# files no release builds there, so only the host's diagnostics fail the gate.
# The rest go to gopls-other-platforms.log. A full run reads every Go file,
# which takes minutes, so both modes read the files the branch changed against
# $base. Like lint, it fails only on the lines the branch added; the findings
# on older lines of those files go to gopls-old-lines.log.
check_gopls() {
	local files=() out host f mb=${merge_base:-}
	if [ -z "$mb" ]; then
		mb=$(git merge-base "$base" HEAD 2>/dev/null) || return 0
	fi
	while IFS= read -r f; do
		[ -f "$f" ] && files+=("$f")
	done < <({
		git diff --name-only --diff-filter=d "$mb" -- '*.go'
		git ls-files --others --exclude-standard '*.go'
	} | sort -u)
	[ ${#files[@]} -eq 0 ] && return 0
	out=$($GOPLS check "${files[@]}" 2>&1 | grep -v '^go: downloading ')
	[ -z "$out" ] && return 0
	grep ' \[[a-z0-9,]*\]$' <<<"$out" >"$log_dir/gopls-other-platforms.log"
	host=$(grep -v $'^\t' <<<"$out" | grep -v ' \[[a-z0-9,]*\]$' | sed "s|^$root/||")
	[ -z "$host" ] && return 0
	# added prints "file:line" for every line the branch added or changed.
	added() {
		local f
		for f in "${files[@]}"; do
			if git ls-files --error-unmatch "$f" >/dev/null 2>&1 && git cat-file -e "$mb:$f" 2>/dev/null; then
				git diff -U0 "$mb" -- "$f" | awk -v f="$f" '
					/^@@/ {
						split($3, a, ","); start = substr(a[1], 2); n = (a[2] == "") ? 1 : a[2]
						for (i = 0; i < n; i++) print f ":" start + i
					}'
			else
				awk -v f="$f" '{ print f ":" NR }' "$f"
			fi
		done
	}
	local new
	new=$(awk -F: 'NR == FNR { seen[$1 ":" $2] = 1; next } ($1 ":" $2) in seen' <(added) <(echo "$host"))
	awk -F: 'NR == FNR { seen[$1 ":" $2] = 1; next } !(($1 ":" $2) in seen)' <(added) <(echo "$host") >"$log_dir/gopls-old-lines.log"
	[ -z "$new" ] && return 0
	echo "$new"
	return 1
}

check_tidy() {
	local proxy rc=0 mod
	proxy=$(mktemp -d)
	# The same GOPROXY CI uses: each library at its pinned commit, from this
	# clone, so the tidy check needs no network for it.
	if ! scripts/library-pins.sh proxy "$proxy" >/dev/null; then
		rm -rf "$proxy"
		return 1
	fi
	for mod in "${modules[@]}"; do
		module_touched "$mod" || continue
		if ! (cd "$mod" && GOPROXY="file://$proxy,$(go env GOPROXY)" GONOSUMDB=tuios.dev GOWORK=off go mod tidy -diff); then
			echo "$mod/go.mod: not tidy. Run: just tidy"
			rc=1
		fi
	done
	rm -rf "$proxy"
	scripts/library-pins.sh check >/dev/null || {
		echo "library pins: do not match their folders. Run: scripts/library-pins.sh check"
		rc=1
	}
	return $rc
}

check_cross() {
	local goos rc=0
	for goos in "${cross_goos[@]}"; do
		GOOS=$goos in_build_modules go build -o /dev/null || {
			echo "go build failed for GOOS=$goos"
			rc=1
		}
	done
	if [ -n "$ghostty_pc" ] && [ "$changed" = 0 ]; then
		PKG_CONFIG_PATH=$ghostty_pc in_build_modules go build -tags ghostty -o /dev/null || rc=1
	fi
	return $rc
}

check_deadcode() {
	local out
	# The binaries and every test are the roots, so a helper only a test
	# calls does not count as dead. pkg/ is the public API for embedders, and
	# nothing in this module has to call it. deadcode reads the host
	# platform only, so a function only windows or js/wasm calls shows up
	# here too.
	out=$($DEADCODE -test ./... 2>&1 | grep -v '^go: ' | grep -v '^pkg/')
	[ -z "$out" ] && return 0
	if [ "$changed" = 1 ]; then
		local f hits=""
		for f in "${changed_go[@]}"; do
			hits+=$(grep -F "$f:" <<<"$out")$'\n'
		done
		out=$(grep . <<<"$hits")
		[ -z "$out" ] && return 0
	fi
	echo "$out"
	return 1
}

# ---------------------------------------------------------------- run

if [ "$changed" = 1 ]; then
	echo "check: ${#changed_go[@]} changed Go files against $base"
	if [ ${#changed_files[@]} -eq 0 ]; then
		echo "check: nothing changed against $base"
		exit 0
	fi
else
	echo "check: every module"
fi
[ -z "$ghostty_pc" ] && echo "check: libghostty-vt is not built, so the ghostty build is skipped"

run fmt gate check_fmt
run vet gate check_vet
run lint gate check_lint
run tuioslint gate check_tuioslint
run vuln gate check_vuln
run fix report check_fix
run gopls gate check_gopls
run tidy gate check_tidy
run cross gate check_cross
run deadcode report check_deadcode

echo
printf '%-10s %-7s %7s %7s\n' check result lines time
fail=0
for i in "${!names[@]}"; do
	printf '%-10s %-7s %7s %7s\n' "${names[$i]}" "${results[$i]}" "${counts[$i]}" "${times[$i]}"
	[ "${results[$i]}" = FAIL ] && fail=1
done
echo "logs: $log_dir"
exit $fail
