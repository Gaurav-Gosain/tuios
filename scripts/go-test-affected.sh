#!/usr/bin/env bash
# go-test-affected.sh PATTERN...
#
# Prints the packages among PATTERN whose tests a pull request can affect, one
# import path per line: a package is affected when it changed, or when its
# test binary builds a package that changed (go list -test -deps). Prints
# "all" when the change can affect any package, and nothing when it affects
# none.
#
# The change is BASE to HEAD, with BASE from $AFFECTED_BASE. Without it, on a
# pull_request event, the checkout is the merge of the head into the base,
# so BASE is HEAD^1. Every other event prints "all": main, the nightly and
# a manual run test every package.
#
# A file changes the package of the nearest folder above it that holds Go
# files, so testdata and embedded files count for their package. A file that
# is not Go code also changes every package with a test file that names it,
# or, outside any package, names its top folder. Every
# package runs on a change to a go.mod, go.sum, go.work or go.work.sum beyond
# the pins of tuios.dev libraries, to
# .github/workflows/go-test.yml, to .github/actions, to this script, to
# scripts/go-test-lanes.sh or scripts/retry-flaky.sh, when a package folder
# was removed, and when the diff or go list fails.
set -uo pipefail

patterns=("$@")

all() {
	echo all
	exit 0
}

base=${AFFECTED_BASE:-}
if [ -z "$base" ]; then
	[ "${GITHUB_EVENT_NAME:-}" = pull_request ] || all
	[ "$(git rev-list --parents -n 1 HEAD | wc -w)" -eq 3 ] || all
	base=HEAD^1
fi

changed=$(git diff --name-only "$base" HEAD) || all
[ -n "$changed" ] || exit 0

if grep -qE '^\.github/workflows/go-test\.yml$|^\.github/actions/|^scripts/(go-test-affected|go-test-lanes|retry-flaky)\.sh$' <<<"$changed"; then
	all
fi

# A module file counts only when it changes more than the pins of this
# repository's libraries (tuios.dev/...). Under go.work the libraries build
# from their folders, whose own changes are in the diff, and the libraries
# workflow holds each pin to its folder.
while IFS= read -r f; do
	case $f in
	go.mod | */go.mod | go.sum | */go.sum | go.work | go.work.sum) ;;
	*) continue ;;
	esac
	if git diff -U0 "$base" HEAD -- "$f" | grep -E '^[-+]' | grep -vE '^(\+\+\+|---)' |
		grep -vE '^[-+][[:space:]]*$' | grep -qv 'tuios\.dev/'; then
		all
	fi
done <<<"$changed"

# The package folders the change touched.
declare -A dirs=() readdirs=()
uptree=$(git grep -lE -e '\.\./|"\.\.", "\.\."' -- '*_test.go' ':!e2e' 2>/dev/null)
while IFS= read -r f; do
	d=$(dirname "$f")
	# A Go file in a folder that is gone: the package went, and what
	# imported it changed too.
	if [ "${f%.go}" != "$f" ] && [ ! -d "$d" ]; then
		all
	fi
	while :; do
		if compgen -G "$d/*.go" >/dev/null; then
			dirs[$d]=1
			break
		fi
		[ "$d" != . ] || break
		d=$(dirname "$d")
	done
	# Tests also read files of other folders: cmd/tuios checks
	# docs/CLI_REFERENCE.md, internal/server reads internal/app's source and
	# internal/lint walks the whole tree. A test file that reaches up the tree
	# (a "../" or a "..", ".." path) and names the changed file, its folder or
	# its top folder makes its own package count.
	parent=$(basename "$(dirname "$f")")
	top=${f%%/*}
	while IFS= read -r r; do
		[ -n "$r" ] || continue
		if grep -qF -e "$(basename "$f")" "$r" ||
			{ [ "$top" != "$f" ] && grep -qE -e "[\"/]($top|$parent)[\"/]" "$r"; }; then
			readdirs[$(dirname "$r")]=1
		fi
	done <<<"$uptree"
done <<<"$changed"
[ "${#dirs[@]}" -gt 0 ] || [ "${#readdirs[@]}" -gt 0 ] || exit 0

# Their import paths. A folder of a module the patterns do not name, such as
# e2e/tui, imports nothing the patterns test.
declare -A hit=()
for d in "${!dirs[@]}"; do
	if p=$(go list -f '{{.ImportPath}}' "./$d" 2>/dev/null); then
		hit[$p]=1
	fi
done
# A package whose tests read a changed file counts on its own. The file is
# not compiled in, so the packages that import it do not.
declare -A affected=() named=()
while IFS= read -r p; do
	named[$p]=1
done < <(go list "${patterns[@]}" 2>/dev/null)
for d in "${!readdirs[@]}"; do
	if p=$(go list -f '{{.ImportPath}}' "./$d" 2>/dev/null) && [ -n "${named[$p]:-}" ]; then
		affected[$p]=1
	fi
done
if [ "${#hit[@]}" -eq 0 ]; then
	[ "${#affected[@]}" -eq 0 ] || printf '%s\n' "${!affected[@]}" | sort
	exit 0
fi

# Each test binary and what it builds. A test variant is listed as
# "pkg [pkg.test]", and so are the packages it recompiles for it.
out=$(go list -test -f '{{.ImportPath}}|{{join .Deps " "}}' "${patterns[@]}" 2>/dev/null) || all
while IFS='|' read -r pkg deps; do
	pkg=${pkg%% *}
	pkg=${pkg%.test}
	pkg=${pkg%_test}
	if [ -n "${hit[$pkg]:-}" ]; then
		affected[$pkg]=1
		continue
	fi
	for dep in $deps; do
		if [ -n "${hit[$dep]:-}" ]; then
			affected[$pkg]=1
			break
		fi
	done
done <<<"$out"
printf '%s\n' "${!affected[@]}" | sort
