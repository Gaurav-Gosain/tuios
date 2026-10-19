#!/usr/bin/env bash
# Keeps the libraries that live in this repository as Go modules of their own
# (tuios.dev/progstatus, and any other module go.work lists) in step with the
# versions the root go.mod pins.
#
#   scripts/library-pins.sh check            # the pins match the folders, and
#                                            # tuios builds without go.work
#   scripts/library-pins.sh check --network  # the same, with each library
#                                            # fetched through tuios.dev
#   scripts/library-pins.sh sync             # after go get of a new pin: point
#                                            # go.work at it, tidy both modules
#   scripts/library-pins.sh proxy DIR        # write a GOPROXY directory that
#                                            # holds each library at its pin
#
# Local builds and CI use go.work, so they build against the folders. `go
# install github.com/Gaurav-Gosain/tuios/cmd/tuios@vX` ignores go.work and uses
# the pins. The two agree only when each pinned version holds the same files
# as the folder. check proves that, then builds tuios with GOWORK=off.
#
# go.work replaces each library at its pinned version with its folder. Without
# that, every build in the workspace fetches the pinned go.mod through
# tuios.dev, so CI would depend on the site being up.
#
# A pin is a pseudo-version (v0.0.0-20261011152938-946aa9e626ac), whose
# commit is the last field, or a release such as v0.1.0, whose commit is the
# tag progstatus/v0.1.0. Either way the commit must be in this clone, so CI
# checks out the full history.
#
# To move a pin, commit the library change first, then:
#
#   GOWORK=off go get tuios.dev/progstatus@<commit or progstatus/vX.Y.Z>
#   scripts/library-pins.sh sync
#
# Without --network, check serves each library from a GOPROXY directory built
# from its pinned commit, so it needs neither tuios.dev nor a pushed tag. The
# go.sum hashes still apply: a zip that is not the one the module proxy would
# serve fails the build. With --network, the libraries come through the
# default GOPROXY and tuios.dev, as for a user. The release runs that form.
set -euo pipefail

cd "$(dirname "$0")/.."
repo=$PWD

# libraries prints "dir module" for each module in go.work other than the app
# and the e2e suite.
libraries() {
	go work edit -json | jq -r '.Use[].DiskPath' | while read -r dir; do
		dir=${dir#./}
		case "$dir" in . | e2e/tui) continue ;; esac
		printf '%s %s\n' "$dir" "$(cd "$dir" && go mod edit -json | jq -r .Module.Path)"
	done
}

# pinned prints the version of module the root go.mod requires.
pinned() {
	go mod edit -json | jq -r --arg m "$1" '.Require[]? | select(.Path == $m) | .Version'
}

# imported reports whether a package of tuios imports module. A library that
# tuios does not import yet has nothing to pin: go mod tidy would drop the
# requirement. It joins go.work so CI tests it, and gets its pin with the
# first change that imports it.
imported() {
	go list -deps -f '{{with .Module}}{{.Path}}{{end}}' ./... | grep -qxF "$1"
}

# replaced prints the version go.work replaces module at, and the folder.
replaced() {
	go work edit -json | jq -r --arg m "$1" '.Replace[]? | select(.Old.Path == $m) | "\(.Old.Version) \(.New.Path)"'
}

# commit_of prints the commit that version of the library in dir names.
commit_of() {
	local dir=$1 version=$2 ref
	if [[ $version =~ [-.][0-9]{14}-([0-9a-f]{12})$ ]]; then
		ref=${BASH_REMATCH[1]}
	else
		ref="refs/tags/$dir/$version"
	fi
	git rev-parse --verify --quiet "$ref^{commit}"
}

# wanted prints "dir module version" for each library version the proxy
# serves: the version tuios pins, and each version another library of the
# workspace requires (loom requires plot before tuios imports either).
wanted() {
	local dir module version libs
	libs=$(libraries)
	while read -r dir module; do
		version=$(pinned "$module")
		if [ -n "$version" ] || imported "$module"; then
			echo "$dir $module $version"
		fi
		while read -r other _; do
			(cd "$other" && go mod edit -json) |
				jq -r --arg m "$module" '.Require[]? | select(.Path == $m) | .Version'
		done <<<"$libs" | sort -u | while read -r version; do
			[ -n "$version" ] && echo "$dir $module $version"
		done
	done <<<"$libs" | sort -u
}

# build_proxy writes a GOPROXY directory with every library at its pin.
build_proxy() {
	local out=$1 dir module version commit stage
	mkdir -p "$out"
	out=$(cd "$out" && pwd)
	while read -r dir module version; do
		if ! commit=$(commit_of "$dir" "$version"); then
			echo "$module@${version:-<no pin>}: no commit for this version in this clone" >&2
			return 1
		fi
		if [[ $module =~ [A-Z] ]]; then
			echo "$module: a module path with capitals needs case escaping, which this script does not do" >&2
			return 1
		fi
		stage=$(mktemp -d)
		mkdir -p "$stage/$module@$version" "$out/$module/@v"
		git archive "$commit" "$dir" | tar -x -C "$stage/$module@$version" --strip-components=1
		(cd "$stage" && zip -q -r -D -X "$out/$module/@v/$version.zip" "$module@$version")
		git show "$commit:$dir/go.mod" >"$out/$module/@v/$version.mod"
		printf '{"Version":"%s","Time":"%s"}\n' "$version" \
			"$(TZ=UTC git log -1 --format=%cd --date=format-local:%Y-%m-%dT%H:%M:%SZ "$commit")" \
			>"$out/$module/@v/$version.info"
		echo "$version" >>"$out/$module/@v/list"
		rm -rf "$stage"
	done < <(wanted)
}

# with_proxy runs a command with GOPROXY set to a directory of the pinned
# libraries in front of the usual proxy.
with_proxy() {
	local tmp status=0
	tmp=$(mktemp -d)
	(cd "$repo" && build_proxy "$tmp") || status=$?
	if [ "$status" -eq 0 ]; then
		GOPROXY="file://$tmp,$(go env GOPROXY)" GONOSUMDB=tuios.dev "$@" || status=$?
	fi
	rm -rf "$tmp"
	return "$status"
}

sync_pins() {
	local dir module version old folder
	while read -r dir module; do
		version=$(pinned "$module")
		if [ -z "$version" ] && ! imported "$module"; then
			echo "go.work: $module is not imported by tuios yet, so it has no pin"
			continue
		fi
		if [ -z "$version" ]; then
			echo "$module: go.mod does not require it. Run: GOWORK=off go get $module@<commit>" >&2
			return 1
		fi
		old="" folder=""
		read -r old folder < <(replaced "$module") || true
		if [ -n "$old" ] && [ "$old" != "$version" ]; then
			go work edit -dropreplace="$module@$old"
		fi
		go work edit -replace="$module@$version=./$dir"
		echo "go.work: $module@$version => ./$dir"
	done < <(libraries)
	with_proxy env GOWORK=off go mod tidy
	(cd e2e/tui && with_proxy env GOWORK=off go mod tidy)
	echo "tidied go.mod and e2e/tui/go.mod"
}

check() {
	local network=${1:-} status=0 dir module version commit old folder
	# go.work repeats the go and toolchain lines of go.mod. A workspace build
	# uses the go.work lines, and go install uses the go.mod ones.
	local mod_go work_go
	# Read from the files: go work edit -json in Go 1.26 leaves the toolchain
	# line out.
	local lines='$1 == "go" { g = $2 } $1 == "toolchain" { t = $2 } END { printf "go %s toolchain %s", g ? g : "-", t ? t : "-" }'
	mod_go=$(awk "$lines" go.mod)
	work_go=$(awk "$lines" go.work)
	if [ "$mod_go" != "$work_go" ]; then
		echo "FAIL go.mod has \"$mod_go\" and go.work has \"$work_go\". Make the go and toolchain lines of go.work match go.mod." >&2
		status=1
	else
		echo "ok   go.work and go.mod agree: $mod_go"
	fi
	while read -r dir module; do
		version=$(pinned "$module")
		if [ -z "$version" ] && ! imported "$module"; then
			echo "ok   $module is not imported by tuios yet, so it has no pin"
			continue
		fi
		if [ -z "$version" ]; then
			echo "FAIL $module: go.mod does not require it. Run: GOWORK=off go get $module@<commit>" >&2
			status=1
			continue
		fi
		if ! commit=$(commit_of "$dir" "$version"); then
			echo "FAIL $module@$version: its commit is not in this clone." >&2
			status=1
			continue
		fi
		if ! git diff --quiet "$commit" -- "$dir"; then
			echo "FAIL $module@$version: $dir/ differs from the pinned commit ${commit:0:12}:" >&2
			git diff --stat "$commit" -- "$dir" >&2
			echo "  Commit the change, then: GOWORK=off go get $module@<that commit> && $0 sync" >&2
			status=1
			continue
		fi
		echo "ok   $module@$version matches $dir/"
		old="" folder=""
		read -r old folder < <(replaced "$module") || true
		if [ "$old" != "$version" ] || [ "$folder" != "./$dir" ]; then
			echo "FAIL go.work replaces $module@${old:-<none>} => ${folder:-<none>}, not $version => ./$dir. Run: $0 sync" >&2
			status=1
			continue
		fi
		echo "ok   go.work replaces $module@$version with ./$dir"
	done < <(libraries)
	# A library that requires another (loom requires plot) at a commit:
	# the commit must be in this clone, hold the same files as the folder,
	# and go.work must replace that version with the folder, since nothing
	# else resolves it. A tagged version such as tml/v0.1.0 resolves alone.
	local other req commit
	while read -r dir module; do
		while read -r other _; do
			req=$( (cd "$other" && go mod edit -json) |
				jq -r --arg m "$module" '.Require[]? | select(.Path == $m) | .Version')
			[ -n "$req" ] || continue
			if ! commit=$(commit_of "$dir" "$req"); then
				echo "FAIL $other requires $module@$req, whose commit is not in this clone." >&2
				status=1
				continue
			fi
			if [[ ! $req =~ [-.][0-9]{14}-[0-9a-f]{12}$ ]]; then
				echo "ok   $other requires $module@$req, a tag"
				continue
			fi
			if ! git diff --quiet "$commit" -- "$dir"; then
				echo "FAIL $other requires $module@$req, but $dir/ differs from ${commit:0:12}. Move the pin in $other." >&2
				status=1
				continue
			fi
			if ! go work edit -json | jq -e --arg m "$module" --arg v "$req" --arg f "./$dir" \
				'any(.Replace[]?; .Old.Path == $m and .Old.Version == $v and .New.Path == $f)' >/dev/null; then
				echo "FAIL go.work does not replace $module@$req (required by $other) with ./$dir." >&2
				status=1
				continue
			fi
			echo "ok   $other requires $module@$req, which matches $dir/ and go.work"
		done < <(libraries)
	done < <(libraries)
	[ "$status" -eq 0 ] || return "$status"

	local run=(env)
	if [ "$network" != "--network" ]; then
		run=(with_proxy env)
	fi
	echo "Building tuios without go.work${network:+ (libraries through the network)}"
	"${run[@]}" GOWORK=off GOFLAGS=-mod=readonly go build -o /dev/null ./cmd/tuios ./cmd/tuios-web
	echo "ok   GOWORK=off go build ./cmd/tuios ./cmd/tuios-web"

	# The workspace also holds e2e/tui, whose requirements can raise a
	# dependency the app shares. The binaries must link the same version of
	# every dependency with and without go.work. The libraries themselves
	# differ only in name (a folder against a pin), which the checks above
	# covered.
	local list='{{with .Module}}{{if not .Main}}{{.Path}} {{.Version}}{{end}}{{end}}'
	local libs with without
	libs=$(libraries | awk '{print $2}' | paste -sd'|' -)
	with=$(go list -deps -f "$list" ./cmd/tuios ./cmd/tuios-web | sort -u | grep -Ev "^($libs) " || true)
	without=$("${run[@]}" GOWORK=off GOFLAGS=-mod=readonly go list -deps -f "$list" ./cmd/tuios ./cmd/tuios-web |
		sort -u | grep -Ev "^($libs) " || true)
	if [ "$with" != "$without" ]; then
		echo "FAIL the workspace changes a dependency of tuios (< with go.work, > without):" >&2
		diff <(echo "$with") <(echo "$without") >&2 || true
		return 1
	fi
	echo "ok   go.work does not change a dependency of tuios"
}

case "${1:-}" in
check) check "${2:-}" ;;
sync) sync_pins ;;
proxy)
	[ -n "${2:-}" ] || {
		echo "usage: $0 proxy DIR" >&2
		exit 2
	}
	build_proxy "$2"
	;;
*)
	echo "usage: $0 check [--network] | sync | proxy DIR" >&2
	exit 2
	;;
esac
