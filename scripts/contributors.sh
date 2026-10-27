#!/bin/sh
# Redraw the README's contributors mosaic, assets/contributors.svg, from
# GitHub's contributors list.
#
# Usage: scripts/contributors.sh [repo]
#   repo: owner/name (default: Gaurav-Gosain/tuios)
#
# The work is in scripts/contributors-mosaic.go, which says who is listed
# and in what order. Bot accounts that GitHub reports as users go in
# .github/contributors-exclude, one login per line. A run with no new
# contributors rewrites nothing.
set -eu

REPO="${1:-Gaurav-Gosain/tuios}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"

cd "$ROOT"
exec go run ./scripts/contributors-mosaic.go "$REPO"
