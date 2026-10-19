#!/usr/bin/env bash
# Lists the pele commits after the commit the port follows, and the files each one changes.
# Usage: scripts/upstream.sh PELE_CLONE
set -euo pipefail
pin=458322a5b8abb1ae57ddf127c32017ab9fb14a92
clone=${1:?usage: scripts/upstream.sh PELE_CLONE}
git -C "$clone" fetch --quiet origin
git -C "$clone" log --reverse --format='%n%h %ad %s' --date=short --name-only "$pin..origin/HEAD" -- src tests/corpus
