#!/bin/sh
# Checks tuios.lua: a syntax check with each Lua on PATH, and the stub test
# under a Lua 5.1 (LuaJIT) when there is one, as Rex runs Lua 5.1.
set -eu
cd "$(dirname "$0")"
ran=0
if command -v luajit >/dev/null 2>&1; then
	luajit -bl tuios.lua >/dev/null
	echo "luajit (Lua 5.1): tuios.lua parses"
	luajit tuios_test.lua
	ran=1
fi
if command -v luac >/dev/null 2>&1; then
	luac -p tuios.lua tuios_test.lua
	echo "$(luac -v 2>&1 | head -1): tuios.lua parses"
	if [ "$ran" = 0 ] && command -v lua >/dev/null 2>&1; then
		lua tuios_test.lua
		ran=1
	fi
fi
if [ "$ran" = 0 ]; then
	echo "no Lua found: install luajit or lua to run the checks" >&2
	exit 1
fi
