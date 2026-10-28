#!/usr/bin/env bash
# Lists the pele commits after the commit the port follows, and the files each one changes.
# Usage: scripts/upstream.sh PELE_CLONE
set -euo pipefail
pin=8aff183be57021b533f319a29341a529be934b08
clone=${1:?usage: scripts/upstream.sh PELE_CLONE}
git -C "$clone" fetch --quiet origin
git -C "$clone" log --reverse --format='%n%h %ad %s' --date=short --name-only "$pin..origin/HEAD" -- src tests/corpus
