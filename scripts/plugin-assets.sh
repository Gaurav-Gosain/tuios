#!/usr/bin/env bash
# plugin-assets.sh VERSION [OUT]: pack plugins/ into the release asset
# tuios-plugins_VERSION.tar.gz under .dev/release, which goreleaser attaches
# and lists in checksums.txt. tuios plugins install NAME reads it. The archive
# holds the tracked files only, in a fixed order with fixed owners and times,
# so the same tree gives the same bytes.
set -euo pipefail
version=${1#v}
out=${2:-.dev/release}
rm -rf "$out"
mkdir -p "$out"
git ls-files -z plugins | sed -z 's|^plugins/||' |
	tar -C plugins --null -T - --sort=name --owner=0 --group=0 --numeric-owner \
		--mtime=@0 --format=gnu -cf - |
	gzip -n -9 >"$out/tuios-plugins_$version.tar.gz"
echo "$out/tuios-plugins_$version.tar.gz"
