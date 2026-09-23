#!/bin/sh
# Prepares the module setup for a js/wasm build without touching go.mod, and
# prints the path of the modfile to pass to go build with -modfile.
#
# Two dependencies do not compile for js/wasm as released, so this copies each
# out of the module cache into .wasm-build/ and patches the copy:
#
#   charm.land/bubbletea/v2  has TTY and signal files for unix and windows
#                            only. The copy gets stubs/bubbletea_tty_js.go.in
#                            as tty_js.go. Upstream: charmbracelet/bubbletea
#                            issue 1410. Drop this half once a release builds
#                            for js.
#   github.com/creack/pty    (under xpty) has no js files, so its unix files
#                            are picked up and fail on missing ioctls. The copy
#                            treats js the way it treats windows: every call
#                            reports ErrUnsupported. The browser build never
#                            opens a pty (panes run on internal/webshell), so
#                            nothing reaches those calls. Drop this half once
#                            creack/pty or xpty builds for js.
#
# go.mod and go.sum are never changed: the replace directives go into a copy,
# .wasm-build/go.wasm.mod, which only the browser build reads.
set -e
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
out="$root/.wasm-build"
export GOWORK=off
cd "$root"
mkdir -p "$out"

bt=$(go list -m -f '{{.Dir}}' charm.land/bubbletea/v2)
rm -rf "$out/bubbletea"
cp -R "$bt" "$out/bubbletea"
chmod -R u+w "$out/bubbletea"
cp "$here/stubs/bubbletea_tty_js.go.in" "$out/bubbletea/tty_js.go"

pty=$(go list -m -f '{{.Dir}}' github.com/creack/pty)
rm -rf "$out/pty"
cp -R "$pty" "$out/pty"
chmod -R u+w "$out/pty"
for f in "$out"/pty/*.go; do
	sed -i.bak -e 's|^//go:build !windows\(.*\)$|//go:build !windows \&\& !js\1|' \
		-e 's|^//go:build windows$|//go:build windows \|\| js|' \
		-e '/^\/\/ +build/d' "$f"
done
rm -f "$out"/pty/*.bak
# The _windows suffix limits a file to windows whatever its build line says,
# so the unsupported StartWithSize goes in again under a js name.
cp "$out/pty/start_windows.go" "$out/pty/start_js.go"

cp go.mod "$out/go.wasm.mod"
cp go.sum "$out/go.wasm.sum"
# -modfile resolves relative replace paths against the main module root.
printf '\nreplace charm.land/bubbletea/v2 => ./.wasm-build/bubbletea\n' >>"$out/go.wasm.mod"
printf 'replace github.com/creack/pty => ./.wasm-build/pty\n' >>"$out/go.wasm.mod"
echo "$out/go.wasm.mod"
