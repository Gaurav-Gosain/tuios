#!/usr/bin/env bash
# apt-install.sh [PKG...]: install packages with apt on a CI runner, and
# survive security.ubuntu.com not resolving.
#
# On some days the runner's resolver cannot resolve any ubuntu.com name, and
# apt-get update only warns while the install fails. So the script first gives
# the runner public resolvers when the apt hosts do not resolve. Only if
# security.ubuntu.com still does not resolve it points the apt sources at
# archive.ubuntu.com, which serves the -security pocket too.
#
# With no package it only repairs the sources, for a tool that calls apt
# itself, such as playwright install --with-deps. Run it before that tool.
#
# What it did is printed as "apt-install: path ...", one of:
#   resolves        the apt hosts resolved
#   resolver-fixed  they did not, and they do after the runner got public
#                   resolvers (resolvectl, or /etc/resolv.conf)
#   rewritten       security.ubuntu.com still did not resolve, so it was
#                   replaced before the first try
#   retried         the first try failed, the sources were replaced, the
#                   second try ran
# APT_DIR (default /etc/apt) is the folder whose files are rewritten, and
# RESOLV_CONF (default /etc/resolv.conf) the file written as the last resort.
set -uo pipefail

apt_dir=${APT_DIR:-/etc/apt}
bad=security.ubuntu.com
good=archive.ubuntu.com
resolv_conf=${RESOLV_CONF:-/etc/resolv.conf}
resolvers=(1.1.1.1 8.8.8.8)
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

resolves() { getent hosts "$1" >/dev/null 2>&1; }

# fix_resolver gives the runner public resolvers: through systemd-resolved on
# the default route's interface when resolvectl exists, else by replacing
# resolv.conf.
fix_resolver() {
	local iface
	if command -v resolvectl >/dev/null 2>&1; then
		iface=$(ip route show default 2>/dev/null | awk '{for (i = 1; i < NF; i++) if ($i == "dev") {print $(i + 1); exit}}')
		if [ -n "$iface" ]; then
			sudo resolvectl dns "$iface" "${resolvers[@]}" || true
			sudo resolvectl flush-caches || true
			resolves "$good" && return 0
		fi
	fi
	printf 'nameserver %s\n' "${resolvers[@]}" | sudo tee "$resolv_conf.new" >/dev/null &&
		sudo mv -f "$resolv_conf.new" "$resolv_conf"
}

if resolves "$bad" && resolves "$good"; then
	path=resolves
else
	fix_resolver
	if resolves "$bad" && resolves "$good"; then
		path=resolver-fixed
	else
		rewrite
		path=rewritten
	fi
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
echo "apt-install: the install failed, so the resolver is checked, $bad is replaced by $good and the install tried once more" >&2
resolves "$good" || fix_resolver
rewrite
if install "$@"; then
	echo "apt-install: path retried"
	exit 0
fi
echo "apt-install: path retried, and the install still failed" >&2
exit 1
