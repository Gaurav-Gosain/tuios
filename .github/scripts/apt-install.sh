#!/usr/bin/env bash
# apt-install.sh [PKG...]: install packages with apt on a CI runner, and
# survive security.ubuntu.com not resolving.
#
# GitHub's Ubuntu runner images list security.ubuntu.com for the -security
# pocket. When its name does not resolve, apt-get update only warns, and the
# install then fails on every package that the security pocket holds. The same
# archive.ubuntu.com serves the -security pocket too, so the fallback points
# the apt sources at it.
#
# With no package it only repairs the sources, for a tool that calls apt
# itself, such as playwright install --with-deps. Run it before that tool.
#
# What it did is printed as "apt-install: path ...", one of:
#   resolves       the sources were fine
#   rewritten      security.ubuntu.com did not resolve, so it was replaced
#                  before the first try
#   retried        the first try failed, the sources were replaced, the
#                  second try ran
# APT_DIR (default /etc/apt) is the folder whose files are rewritten.
set -uo pipefail

apt_dir=${APT_DIR:-/etc/apt}
bad=security.ubuntu.com
good=archive.ubuntu.com
opts=(-o Acquire::Retries=3 -o Acquire::http::Timeout=20 -o Acquire::https::Timeout=20)

rewritten=0
rewrite() {
	local files
	files=$(grep -rl "$bad" "$apt_dir" 2>/dev/null || true)
	[ -z "$files" ] && return 0
	# shellcheck disable=SC2086 # one file per line, none with a space
	sudo sed -i.aptbak "s/$bad/$good/g" $files
	for f in $files; do sudo rm -f "$f.aptbak"; done
	rewritten=1
}

install() {
	sudo apt-get "${opts[@]}" update -qq && sudo apt-get "${opts[@]}" install -y -qq --no-install-recommends "$@"
}

if getent hosts "$bad" >/dev/null 2>&1; then
	path=resolves
else
	rewrite
	path=rewritten
fi

if [ $# -eq 0 ]; then
	echo "apt-install: path $path"
	exit 0
fi

if install "$@"; then
	echo "apt-install: path $path"
	exit 0
fi

if [ "$rewritten" = 1 ]; then
	echo "apt-install: path $path, and the install still failed" >&2
	exit 1
fi
echo "apt-install: the install failed, so $bad is replaced by $good and tried once more" >&2
rewrite
if install "$@"; then
	echo "apt-install: path retried"
	exit 0
fi
echo "apt-install: path retried, and the install still failed" >&2
exit 1
