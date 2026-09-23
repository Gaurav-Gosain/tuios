#!/bin/sh
# Prepares the module setup for a js/wasm build without touching go.mod.
#
# Bubble Tea v2 has TTY and signal files only for unix and windows, so it does
# not compile for js/wasm. This copies the module out of the module cache into
# .wasm-build/, adds the js stubs from overlay/, and writes a go.wasm.mod with a
# replace directive. build.sh passes it with -modfile.
set -e
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
out="$root/.wasm-build"
cd "$root"
bt=$(go list -m -f '{{.Dir}}' charm.land/bubbletea/v2)
rm -rf "$out/bubbletea"
mkdir -p "$out"
cp -R "$bt" "$out/bubbletea"
chmod -R u+w "$out/bubbletea"
cp "$here/overlay/bubbletea_tty_js.go" "$out/bubbletea/tty_js.go"
# creack/pty (under xpty) has no js files and fails on missing ioctls. The
# copy treats js the way it treats windows: every pty call reports
# ErrUnsupported. The wasm build never opens a pty; panes run the fake shell.
cp_pty=$(go list -m -f '{{.Dir}}' github.com/creack/pty)
rm -rf "$out/pty"
cp -R "$cp_pty" "$out/pty"
chmod -R u+w "$out/pty"
for f in "$out"/pty/*.go; do
	sed -i.bak -e 's|^//go:build !windows\(.*\)$|//go:build !windows \&\& !js\1|' \
		-e 's|^//go:build windows$|//go:build windows \|\| js|' \
		-e '/^\/\/ +build/d' "$f"
done
rm -f "$out"/pty/*.bak
# The _windows suffix constrains the file to windows whatever its build line
# says, so the unsupported StartWithSize goes in under a js name.
cp "$out/pty/start_windows.go" "$out/pty/start_js.go"

cp go.mod "$out/go.wasm.mod"
cp go.sum "$out/go.wasm.sum"
printf '\nreplace charm.land/bubbletea/v2 => ./.wasm-build/bubbletea\n' >> "$out/go.wasm.mod"
printf 'replace github.com/creack/pty => ./.wasm-build/pty\n' >> "$out/go.wasm.mod"
# -modfile resolves relative replace paths against the main module root.
echo "$out/go.wasm.mod"
