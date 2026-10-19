# Dev commands for working on tuios. Run `just` or `just --list` for the list.
#
# Most recipes never touch the daemon you work in. The ones that do (install,
# install-ghostty, deploy, deploy-hosts, restart) say so in their comment.
# The others run against a scratch daemon whose sockets, state and config live
# under the scratch directory below, or against none at all.
#
# Override a variable on the command line, for example:
#   just NICE="nice -n 19" test
#   just CONFIG="$HOME/.config/tuios/config.toml" sandbox

set shell := ["bash", "-cu"]
set positional-arguments

# Test and build runs share the machine with the person using it. taskset pins
# the whole process tree, daemons and shells the e2e suite spawns included, to
# half the cores. GOMAXPROCS and `go test -p` only bind the Go runtime.
NICE := "nice -n 10"
TASKSET := `n=$(nproc 2>/dev/null || echo 1); if command -v taskset >/dev/null && [ "$n" -gt 1 ]; then echo "taskset -c 0-$((n / 2 - 1))"; fi`

# Where `install` puts the binaries. install.sh reads the same variable.
PREFIX := env("TUIOS_PREFIX", home_directory() / ".local/bin")

# An absolute path to a config.toml to copy into the scratch daemon's config.
# Empty means the defaults. A [hosts] table in the file links the scratch
# daemon to those machines.
CONFIG := ""

# The golangci-lint that lint runs: the version in .github/workflows/lint.yml,
# built with the local Go. A binary built with an older Go fails to type-check.
GOLANGCI_LINT := "go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0"

# How many times bench-save runs each benchmark.
BENCH_COUNT := "6"

root := justfile_directory()
out := root / ".dev"
bin := out / "bin"
bench_dir := out / "bench"
scratch := env("TMPDIR", "/tmp") / "tuios-dev-" + `id -u`

# The daemon log of the daemon you work in (internal/session/logsink.go).
daemon_log := if os() == "macos" { env("XDG_STATE_HOME", home_directory() / "Library/Application Support") / "tuios/daemon.log" } else { env("XDG_STATE_HOME", home_directory() / ".local/state") / "tuios/daemon.log" }

# Points every directory tuios reads or writes at $dir, and drops the variables
# a pane sets, so a command run from inside a tuios pane cannot reach the
# daemon that owns the pane. Used by the shebang recipes below.
_isolate := '''
isolate() {
	local dir=$1
	mkdir -p "$dir"/{run,config/tuios,state,cache,data,tmp}
	chmod 700 "$dir/run"
	export XDG_RUNTIME_DIR="$dir/run" XDG_CONFIG_HOME="$dir/config" \
		XDG_CONFIG_DIRS="$dir/config" XDG_STATE_HOME="$dir/state" \
		XDG_CACHE_HOME="$dir/cache" XDG_DATA_HOME="$dir/data" TMPDIR="$dir/tmp"
	unset TUIOS_SESSION TUIOS_PANE_ID TUIOS_PANE_TOKEN TUIOS_WINDOW_ID \
		TUIOS_SOCKET TUIOS_HOST TUIOS_SSH
	if [ -n "${CONFIG:-}" ]; then
		cp "$CONFIG" "$dir/config/tuios/config.toml"
	fi
}
'''

# List the recipes
[private]
default:
    @{{ just_executable() }} --justfile {{ quote(justfile()) }} --list --unsorted

# ---------------------------------------------------------------- build

# Build tuios and tuios-web into .dev/bin as dev+COMMIT
[group('build')]
build:
    #!/usr/bin/env bash
    set -euo pipefail
    cd {{ quote(root) }}
    ldflags='-s -w -X main.builtBy=just'
    version=dev
    if rev=$(git rev-parse HEAD 2>/dev/null); then
        version="dev+${rev:0:12}"
        if [ -n "$(git status --porcelain)" ]; then
            rev="$rev-dirty"
            version="$version-dirty"
        fi
        ldflags="$ldflags -X main.commit=$rev -X main.date=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    fi
    ldflags="$ldflags -X main.version=$version"
    mkdir -p {{ quote(bin) }}
    {{ NICE }} {{ TASKSET }} go build -trimpath -ldflags "$ldflags" -o {{ quote(bin / "tuios") }} ./cmd/tuios
    {{ NICE }} {{ TASKSET }} go build -trimpath -ldflags "$ldflags" -o {{ quote(bin / "tuios-web") }} ./cmd/tuios-web
    {{ quote(bin / "tuios") }} --version | head -1

# The live daemon keeps the old build until `just restart`.
# Install tuios and tuios-web to ~/.local/bin, keep a .bak
[group('build')]
install: _backup
    {{ quote(root / "scripts/install.sh") }} --prefix {{ quote(PREFIX) }} --keep-server

# The live daemon keeps the old build until `just restart`.
# Install with the libghostty-vt emulator (needs zig)
[group('build')]
install-ghostty: _backup
    {{ quote(root / "scripts/install.sh") }} ghostty --prefix {{ quote(PREFIX) }} --keep-server

[private]
_backup:
    #!/usr/bin/env bash
    set -euo pipefail
    for name in tuios tuios-web; do
        if [ -f {{ quote(PREFIX) }}/"$name" ]; then
            cp -p {{ quote(PREFIX) }}/"$name" {{ quote(PREFIX) }}/"$name.bak"
            echo "kept {{ PREFIX }}/$name.bak"
        fi
    done

# Uses `tuios hosts sync`. With no names it syncs every host in the [hosts]
# table. A restart ends the programs in the panes of that daemon.
# Install here and on hosts, restart old daemons (ends pane programs)
[group('deploy')]
deploy *hosts: build
    {{ quote(bin / "tuios") }} hosts sync --dev --src {{ quote(root) }} --local --restart --yes "$@"

# Install on hosts only, restart old daemons (ends pane programs)
[group('deploy')]
deploy-hosts *hosts: build
    {{ quote(bin / "tuios") }} hosts sync --dev --src {{ quote(root) }} --restart --yes "$@"

# Layouts come back with new shells.
# Restart the live daemon (ends the programs in every pane)
[group('deploy')]
restart:
    tuios kill-server || true
    tuios start-server
    tuios --version | head -1

# Show the installed version and live sessions (read only)
[group('daemon')]
status:
    tuios --version | head -1
    tuios ls

# Follow the live daemon's log file (read only)
[group('daemon')]
logs lines="100":
    tail -n "$1" -F {{ quote(daemon_log) }}

# Debug logs go to /tmp/tuios-debug.log.
# Run the TUI with --debug on a scratch daemon
[group('daemon')]
run-debug *args: build
    #!/usr/bin/env bash
    set -euo pipefail
    {{ _isolate }}
    CONFIG={{ quote(CONFIG) }} isolate {{ quote(scratch / "debug") }}
    exec {{ quote(bin / "tuios") }} --debug "$@"

# With no args it attaches session "sandbox". With args it runs that
# subcommand, for example `just sandbox ls`.
# Run tuios against a throwaway daemon
[group('daemon')]
sandbox *args: build
    #!/usr/bin/env bash
    set -euo pipefail
    {{ _isolate }}
    CONFIG={{ quote(CONFIG) }} isolate {{ quote(scratch / "sandbox") }}
    if [ $# -eq 0 ]; then
        set -- attach --create sandbox
    fi
    exec {{ quote(bin / "tuios") }} "$@"

# Use it in bash or zsh: eval "$(just sandbox-env)"
# Print the environment of the throwaway daemon
[group('daemon')]
sandbox-env:
    #!/usr/bin/env bash
    set -euo pipefail
    {{ _isolate }}
    isolate {{ quote(scratch / "sandbox") }} >/dev/null
    for v in XDG_RUNTIME_DIR XDG_CONFIG_HOME XDG_CONFIG_DIRS XDG_STATE_HOME XDG_CACHE_HOME XDG_DATA_HOME TMPDIR; do
        printf 'export %s=%q\n' "$v" "${!v}"
    done
    echo 'unset TUIOS_SESSION TUIOS_PANE_ID TUIOS_PANE_TOKEN TUIOS_WINDOW_ID TUIOS_SOCKET TUIOS_HOST TUIOS_SSH'
    printf 'export PATH=%q:"$PATH"\n' {{ quote(bin) }}

# Stop the scratch daemons of sandbox, run-debug and web
[group('daemon')]
sandbox-stop:
    #!/usr/bin/env bash
    set -uo pipefail
    {{ _isolate }}
    tuios={{ quote(bin / "tuios") }}
    command -v "$tuios" >/dev/null || tuios=tuios
    for dir in {{ quote(scratch) }}/*/; do
        [ -S "$dir/run/tuios/tuios.sock" ] || continue
        ( isolate "${dir%/}" && "$tuios" kill-server && echo "stopped ${dir%/}" )
    done

# Serve tuios-web on 127.0.0.1 against a scratch daemon
[group('daemon')]
web port="7681" *args: build
    #!/usr/bin/env bash
    set -euo pipefail
    {{ _isolate }}
    CONFIG={{ quote(CONFIG) }} isolate {{ quote(scratch / "web") }}
    port=$1
    shift
    exec {{ quote(bin / "tuios-web") }} --host 127.0.0.1 --port "$port" "$@"

# Build Learn tuios (wasm) and serve it on 127.0.0.1
[group('daemon')]
learn port="8765":
    {{ quote(root / "cmd/tuios-wasm/build.sh") }} {{ quote(out / "learn") }}
    node {{ quote(root / "cmd/tuios-wasm/serve.mjs") }} {{ quote(out / "learn") }} "$1"

# Args go to the launcher. Its --live mode uses the live daemon.
# Launch tuios-gui on its own empty daemon
[group('daemon')]
gui *args:
    #!/usr/bin/env bash
    set -euo pipefail
    if ! command -v tuios-gui >/dev/null; then
        echo "tuios-gui is not on PATH. Install the GPUI app first." >&2
        exit 1
    fi
    exec tuios-gui "$@"

# ---------------------------------------------------------------- test

# Run the unit tests of every module
[group('test')]
test *args:
    {{ NICE }} {{ TASKSET }} go test ./... ./progstatus/... ./vt/... ./swatch/... ./shot/... ./tml/... ./plot/... ./loom/... ./sketch/... -timeout 20m "$@"

# Example: just test-pkg ./vt/ -run TestX
# Run the unit tests of one package
[group('test')]
test-pkg pkg *args:
    {{ NICE }} {{ TASKSET }} go test "$@"

# Run the e2e suite, or the tests that match a pattern
[group('test')]
e2e pattern="" *args:
    #!/usr/bin/env bash
    set -euo pipefail
    cd {{ quote(root) }}
    mkdir -p {{ quote(out / "e2e") }}
    go build -o {{ quote(out / "e2e/tuios") }} ./cmd/tuios
    cd e2e/tui
    pattern=$1
    shift
    TUIOS_E2E=1 TUIOS_E2E_BIN={{ quote(out / "e2e/tuios") }} \
        {{ NICE }} {{ TASKSET }} go test -count=1 -timeout 40m ${pattern:+-run "$pattern"} "$@" .

# Logs of failed runs go to .dev/flake.
# Run one e2e test N times and count the failures
[group('test')]
e2e-flake test n="20":
    #!/usr/bin/env bash
    set -uo pipefail
    cd {{ quote(root) }}
    mkdir -p {{ quote(out / "e2e") }} {{ quote(out / "flake") }}
    go build -o {{ quote(out / "e2e/tuios") }} ./cmd/tuios || exit 1
    cd e2e/tui
    TUIOS_E2E=1 TUIOS_E2E_BIN={{ quote(out / "e2e/tuios") }} \
        {{ NICE }} {{ TASKSET }} go test -c -o {{ quote(out / "e2e/e2e.test") }} . || exit 1
    if ! {{ quote(out / "e2e/e2e.test") }} -test.list "^$1\$" | grep -qx "$1"; then
        echo "No e2e test named $1." >&2
        exit 1
    fi
    failed=0
    for i in $(seq 1 "$2"); do
        log={{ quote(out / "flake") }}/"$1-$i.log"
        if TUIOS_E2E=1 TUIOS_E2E_BIN={{ quote(out / "e2e/tuios") }} \
            {{ NICE }} {{ TASKSET }} {{ quote(out / "e2e/e2e.test") }} -test.run "^$1\$" -test.count 1 -test.timeout 10m >"$log" 2>&1; then
            rm -f "$log"
            echo "run $i: pass"
        else
            failed=$((failed + 1))
            echo "run $i: FAIL ($log)"
        fi
    done
    echo "$1: $failed of $2 runs failed"
    [ "$failed" -eq 0 ]

# Run the unit tests with the race detector
[group('test')]
race *args:
    {{ NICE }} {{ TASKSET }} go test -race ./... ./progstatus/... ./vt/... ./swatch/... ./shot/... ./tml/... ./plot/... ./loom/... ./sketch/... -timeout 80m "$@"

# Builds libghostty-vt on first use. Needs zig.
# Run the tests of the ghostty backend
[group('test')]
ghostty-test *args:
    #!/usr/bin/env bash
    set -euo pipefail
    cd {{ quote(root) }}
    cache=${GHOSTTY_VT_CACHE:-$PWD/.ghostty-vt}
    scripts/ghostty-lib.sh native
    PKG_CONFIG_PATH="$cache/native/pkgconfig" \
        {{ NICE }} {{ TASKSET }} go test -tags ghostty -count=1 -short \
        ./vt/ ./internal/session/ ./internal/terminal/ \
        ./internal/app/ ./internal/input/ ./cmd/tuios/ "$@"

# Compare the emulator with tmux (needs tmux)
[group('test')]
differential:
    {{ NICE }} {{ TASKSET }} go test -tags differential ./vt/ -run TestDifferential -v

# Example: just fuzz FuzzEmulatorScript 10m
# Run one fuzz target
[group('test')]
fuzz target time="1m":
    #!/usr/bin/env bash
    set -euo pipefail
    cd {{ quote(root) }}
    file=$(git grep -l "^func $1(" -- '*_test.go' | head -1 || true)
    if [ -z "$file" ]; then
        echo "No fuzz target named $1. List them with: go test -list Fuzz ./..." >&2
        exit 1
    fi
    {{ NICE }} {{ TASKSET }} go test "./$(dirname "$file")/" -run '^$' -fuzz "^$1\$" -fuzztime "$2"

# Run the benchmarks that match a pattern
[group('perf')]
bench pattern="." pkg="./internal/app/" *args:
    {{ NICE }} {{ TASKSET }} go test "$2" -run '^$' -bench "$1" -benchmem "${@:3}"

# Save to .dev/bench/NAME.txt. BENCH_COUNT sets the runs (6).
# Run benchmarks and save them for bench-compare
[group('perf')]
bench-save name pattern="." pkg="./internal/app/":
    mkdir -p {{ quote(bench_dir) }}
    {{ NICE }} {{ TASKSET }} go test "$3" -run '^$' -bench "$2" -benchmem -count {{ BENCH_COUNT }} | tee {{ quote(bench_dir) }}/"$1.txt"

# Compare two saved benchmark runs with benchstat
[group('perf')]
bench-compare a b:
    cd {{ quote(bench_dir) }} && benchstat "$1=$1.txt" "$2=$2.txt"

# Check the binary size budget
[group('perf')]
size *targets:
    {{ NICE }} {{ TASKSET }} {{ quote(root / "scripts/binary-size.sh") }} "$@"

# ---------------------------------------------------------------- check

# golangci-lint at the CI version, go vet for linux, darwin, windows,
# freebsd and openbsd, and the internal/lint checks.
# Run the linters and go vet for every OS
[group('check')]
lint:
    #!/usr/bin/env bash
    set -euo pipefail
    cd {{ quote(root) }}
    {{ NICE }} {{ TASKSET }} {{ GOLANGCI_LINT }} run --timeout 20m
    for dir in progstatus vt swatch shot tml plot loom sketch; do (cd "$dir" && {{ NICE }} {{ TASKSET }} {{ GOLANGCI_LINT }} run --timeout 10m); done
    for goos in linux darwin windows freebsd openbsd; do
        echo "go vet ($goos)"
        GOOS=$goos {{ NICE }} {{ TASKSET }} go vet ./... ./progstatus/... ./vt/... ./swatch/... ./shot/... ./tml/... ./plot/... ./loom/... ./sketch/...
    done
    {{ NICE }} {{ TASKSET }} go test -count=1 ./internal/lint/

# Format every tracked Go file
[group('check')]
fmt:
    git -C {{ quote(root) }} ls-files -z '*.go' | (cd {{ quote(root) }} && xargs -0 gofmt -w)

# Fail if a tracked Go file is not formatted
[group('check')]
fmt-check:
    #!/usr/bin/env bash
    set -euo pipefail
    cd {{ quote(root) }}
    bad=$(git ls-files -z '*.go' | xargs -0 gofmt -l)
    if [ -n "$bad" ]; then
        echo "These files need gofmt (run: just fmt):"
        echo "$bad"
        exit 1
    fi

# Tidy every go.mod, then check the library pins
[group('check')]
tidy: (_tidy "")

# Fail if a go.mod is not tidy
[group('check')]
tidy-check: (_tidy "-diff")

[private]
_tidy mode:
    #!/usr/bin/env bash
    set -euo pipefail
    cd {{ quote(root) }}
    # The same GOPROXY CI uses: each library at its pinned commit, from this clone.
    proxy={{ quote(out / "libproxy") }}
    rm -rf "$proxy"
    scripts/library-pins.sh proxy "$proxy" >/dev/null
    export GOPROXY="file://$proxy,$(go env GOPROXY)" GONOSUMDB=tuios.dev
    for dir in . e2e/tui progstatus vt swatch shot tml plot loom sketch; do
        echo "go mod tidy $1 ($dir)"
        (cd "$dir" && GOWORK=off go mod tidy $1)
    done
    if [ -z "$1" ]; then
        scripts/library-pins.sh check
    fi

# Check that the library pins match their folders
[group('check')]
pins *args:
    {{ quote(root / "scripts/library-pins.sh") }} check "$@"

# Needs node and Chromium. TUIOS_CHROMIUM names the browser.
# Run the Playwright tests of the web client
[group('check')]
clienttests *args:
    #!/usr/bin/env bash
    set -euo pipefail
    cd {{ quote(root) }}
    mkdir -p {{ quote(bin) }}
    go build -o {{ quote(bin / "tuios-web") }} ./cmd/tuios-web
    cd clienttests
    [ -d node_modules ] || npm ci
    if [ -z "${TUIOS_CHROMIUM:-}" ]; then
        TUIOS_CHROMIUM=$(command -v chromium || command -v google-chrome || command -v google-chrome-stable || true)
        [ -n "$TUIOS_CHROMIUM" ] && export TUIOS_CHROMIUM
    fi
    TUIOS_WEB_BIN={{ quote(bin / "tuios-web") }} {{ NICE }} {{ TASKSET }} npx playwright test "$@"

# Build the wasm build and test its Go side
[group('check')]
wasm-check:
    {{ NICE }} {{ TASKSET }} go test -count=1 ./internal/webshell/ ./internal/learn/
    {{ NICE }} {{ TASKSET }} {{ quote(root / "cmd/tuios-wasm/build.sh") }} {{ quote(out / "learn") }}

# Run the local part of CI: fmt, tidy, lint, test, size
[group('check')]
ci: fmt-check tidy-check lint test size

# ---------------------------------------------------------------- clean

# Stop scratch daemons, remove build output and scratch dirs
[group('clean')]
clean: sandbox-stop
    rm -rf {{ quote(out) }} {{ quote(root / ".wasm-build") }} {{ quote(scratch) }}
