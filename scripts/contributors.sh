#!/bin/sh
# Write the README's contributors grid from GitHub's contributors list.
#
# Usage: scripts/contributors.sh [repo] [readme]
#   repo:   owner/name (default: Gaurav-Gosain/tuios)
#   readme: path to rewrite (default: README.md at the repository root)
#
# Only people are listed. An entry is kept when its type is "User" and its
# login does not end in "[bot]". Bot accounts that GitHub reports as users go
# in .github/contributors-exclude, one login per line.
#
# The grid replaces whatever sits between the contributors:start and
# contributors:end markers. Entries are ordered by contribution count, then by
# login, and the output carries no timestamp, so a run with no new
# contributors rewrites nothing.
set -eu

REPO="${1:-Gaurav-Gosain/tuios}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
README="${2:-$ROOT/README.md}"
EXCLUDE="$ROOT/.github/contributors-exclude"
PER_ROW=10

command -v gh >/dev/null || { echo "contributors.sh: gh not found" >&2; exit 1; }
if ! grep -q '^<!-- contributors:start -->$' "$README" ||
  ! grep -q '^<!-- contributors:end -->$' "$README"; then
  echo "contributors.sh: $README has no contributors markers" >&2
  exit 1
fi

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

gh api --paginate "repos/$REPO/contributors?per_page=100" \
  --jq '.[] | select(.type == "User") | [.login, (.id|tostring), (.contributions|tostring)] | @tsv' \
  > "$WORK/raw"

# The exclude list, lower-cased, without comments or blank lines. GitHub
# logins are case-insensitive.
: > "$WORK/exclude"
if [ -f "$EXCLUDE" ]; then
  sed -e 's/#.*//' -e 's/[[:space:]]//g' "$EXCLUDE" | grep -v '^$' |
    tr '[:upper:]' '[:lower:]' > "$WORK/exclude" || true
fi

# Drop [bot] logins and excluded ones, then sort by count and login. The API
# breaks ties in no documented order, so the second key keeps runs stable.
# The exclude list is read in BEGIN: the NR == FNR idiom misfires when the
# list is empty, because the first file then contributes no records.
awk -F '\t' -v ex="$WORK/exclude" '
  BEGIN { while ((getline line < ex) > 0) skip[line] = 1 }
  $1 ~ /\[bot\]$/ { next }
  tolower($1) in skip { next }
  { print }' "$WORK/raw" |
  LC_ALL=C sort -t "$(printf '\t')" -k3,3nr -k1,1f > "$WORK/people"

[ -s "$WORK/people" ] || { echo "contributors.sh: $REPO returned no contributors" >&2; exit 1; }

awk -F '\t' -v per="$PER_ROW" '
  BEGIN { print "<p align=\"center\">" }
  {
    if (NR > 1 && (NR - 1) % per == 0) print "<br>"
    printf "<a href=\"https://github.com/%s\" title=\"%s\"><img src=\"https://avatars.githubusercontent.com/u/%s?s=128&amp;v=4\" width=\"64\" height=\"64\" alt=\"%s\" title=\"%s\"></a>\n", $1, $1, $2, $1, $1
  }
  END { print "</p>" }' "$WORK/people" > "$WORK/grid"

awk -v grid="$WORK/grid" '
  $0 == "<!-- contributors:start -->" {
    print
    while ((getline line < grid) > 0) print line
    skipping = 1
    next
  }
  $0 == "<!-- contributors:end -->" { skipping = 0 }
  !skipping { print }' "$README" > "$WORK/readme"

COUNT=$(wc -l < "$WORK/people" | tr -d ' ')
if cmp -s "$WORK/readme" "$README"; then
  echo "contributors.sh: $README already lists all $COUNT contributors"
  exit 0
fi
cat "$WORK/readme" > "$README"
echo "contributors.sh: wrote $COUNT contributors to $README"
