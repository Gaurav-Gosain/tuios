#!/bin/sh
# Draw the README's star history chart from GitHub's own stargazer timestamps.
#
# Usage: scripts/star-history.sh [repo] [output.svg]
#   repo:   owner/name (default: Gaurav-Gosain/tuios)
#   output: path to write (default: assets/star-history.svg)
#
# star-history.com used to render this for us and now serves an error image in
# place of a chart; the mirrors that still draw one are months behind. The
# stargazers endpoint still hands out starred_at under the star+json media
# type, so the chart is cheaper to draw here than to depend on. Committing the
# result is also what keeps the README honest: GitHub's camo proxy caches a
# remote image for hours regardless.
#
# The SVG carries no generation timestamp, so a run that finds no new stars
# rewrites the same bytes and leaves nothing for CI to commit.
set -eu

REPO="${1:-Gaurav-Gosain/tuios}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="${2:-$ROOT/assets/star-history.svg}"

command -v gh >/dev/null || { echo "star-history.sh: gh not found" >&2; exit 1; }

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

# --paginate walks the Link header for us; per_page=100 is the endpoint's
# ceiling, so this is one request per hundred stars.
gh api --paginate \
  -H "Accept: application/vnd.github.star+json" \
  "repos/$REPO/stargazers?per_page=100" \
  --jq '.[].starred_at' > "$WORK/raw"

# ISO-8601 in UTC sorts lexically, so ordering the series needs no date parsing.
LC_ALL=C sort "$WORK/raw" > "$WORK/sorted"

[ -s "$WORK/sorted" ] || { echo "star-history.sh: $REPO returned no stargazers" >&2; exit 1; }

# The plot is drawn in awk rather than by a chart library so that regenerating
# it needs nothing but gh and a POSIX box. mawk has no mktime, hence the
# hand-rolled civil/day conversions below.
cat > "$WORK/chart.awk" <<'AWK_EOF'
function floordiv(a, b) { return (a >= 0) ? int(a / b) : -int((-a + b - 1) / b) }

# Howard Hinnant's days_from_civil: a proleptic Gregorian day number with
# 1970-01-01 as day zero, which is all the calendar this chart needs.
function days_from_civil(y, m, d,   era, yoe, doy, doe) {
	y -= (m <= 2)
	era = floordiv(y, 400)
	yoe = y - era * 400
	doy = int((153 * (m + ((m > 2) ? -3 : 9)) + 2) / 5) + d - 1
	doe = yoe * 365 + int(yoe / 4) - int(yoe / 100) + doy
	return era * 146097 + doe - 719468
}

function civil_from_days(z,   era, doe, yoe, doy, mp) {
	z += 719468
	era = floordiv(z, 146097)
	doe = z - era * 146097
	yoe = int((doe - int(doe / 1460) + int(doe / 36524) - int(doe / 146096)) / 365)
	CY = yoe + era * 400
	doy = doe - (365 * yoe + int(yoe / 4) - int(yoe / 100))
	mp = int((5 * doy + 2) / 153)
	CD = doy - int((153 * mp + 2) / 5) + 1
	CM = mp + ((mp < 10) ? 3 : -9)
	CY += (CM <= 2)
}

function comma(n,   s, out) {
	s = sprintf("%d", n)
	out = ""
	while (length(s) > 3) {
		out = "," substr(s, length(s) - 2) out
		s = substr(s, 1, length(s) - 3)
	}
	return s out
}

# Round a raw interval up to the nearest 1, 2 or 5 times a power of ten so the
# y axis lands on numbers a reader can add up in their head.
function nice_step(raw,   e, f) {
	e = 1
	while (raw / e >= 10) e *= 10
	while (raw / e < 1) e /= 10
	f = raw / e
	if (f <= 1) return e
	if (f <= 2) return 2 * e
	if (f <= 5) return 5 * e
	return 10 * e
}

function px(d) { return L + (d - dmin) * W / (dmax - dmin) }
function py(v) { return T + H - v * H / ymax }

BEGIN {
	split("Jan Feb Mar Apr May Jun Jul Aug Sep Oct Nov Dec", MON, " ")
	L = 64; T = 60; W = 716; H = 232
	VW = 800; VH = 340
	ACCENT = "#9551f5"
	# One grey that clears 4.3:1 against both #ffffff and GitHub's #0d1117,
	# which is what lets a single file serve both README themes.
	MUTED = "#75797f"
	n = 0
}

{
	# 0123456789012345678
	# 2025-09-06T19:35:35Z
	n++
	yy = substr($0, 1, 4) + 0
	mm = substr($0, 6, 2) + 0
	dd = substr($0, 9, 2) + 0
	hh = substr($0, 12, 2) + 0
	mi = substr($0, 15, 2) + 0
	ss = substr($0, 18, 2) + 0
	day[n] = days_from_civil(yy, mm, dd) + (hh * 3600 + mi * 60 + ss) / 86400
}

END {
	if (n == 0) exit 1
	dmin = day[1]
	dmax = day[n]
	if (dmax <= dmin) dmax = dmin + 1

	step = nice_step(n / 4)
	ymax = step * (int(n / step) + 1)

	# The chart is ~716px wide, so a few hundred samples is already finer than
	# the raster it lands on. Index 1 and index n are pinned so the curve starts
	# at the first star and ends on the true total.
	target = 360
	if (n < target) target = n
	np = 0
	for (i = 1; i <= target; i++) {
		k = (target == 1) ? n : int(1 + (i - 1) * (n - 1) / (target - 1) + 0.5)
		if (np > 0 && k == pick[np]) continue
		np++
		pick[np] = k
	}
	if (pick[np] != n) { np++; pick[np] = n }

	line = ""
	for (i = 1; i <= np; i++) {
		k = pick[i]
		line = line sprintf("%s%.1f,%.1f", (i == 1 ? "" : " "), px(day[k]), py(k))
	}
	area = sprintf("M %.1f,%.1f L ", px(dmin), py(0)) line sprintf(" L %.1f,%.1f Z", px(dmax), py(0))

	# Ticks on month boundaries rather than at even pixel offsets: a reader
	# scanning the axis is looking for months, not for equal spacing.
	civil_from_days(int(dmin)); m0 = CY * 12 + CM - 1
	if (days_from_civil(CY, CM, 1) < dmin) m0++
	civil_from_days(int(dmax)); m1 = CY * 12 + CM - 1
	span = m1 - m0
	mstep = 1
	split("1 2 3 6 12", CAND, " ")
	for (i = 1; i <= 5; i++) { mstep = CAND[i]; if (span / mstep <= 6) break }

	printf "<svg xmlns=\"http://www.w3.org/2000/svg\" viewBox=\"0 0 %d %d\" width=\"%d\" height=\"%d\" role=\"img\" aria-label=\"%s star history: %s stars\">\n", VW, VH, VW, VH, repo, comma(n)
	printf "<defs><linearGradient id=\"fade\" x1=\"0\" y1=\"0\" x2=\"0\" y2=\"1\">"
	printf "<stop offset=\"0\" stop-color=\"%s\" stop-opacity=\"0.28\"/>", ACCENT
	printf "<stop offset=\"1\" stop-color=\"%s\" stop-opacity=\"0.02\"/>", ACCENT
	printf "</linearGradient></defs>\n"
	printf "<style>text{font-family:ui-sans-serif,system-ui,-apple-system,Segoe UI,Helvetica,Arial,sans-serif}</style>\n"

	printf "<text x=\"%d\" y=\"36\" fill=\"%s\" font-size=\"30\" font-weight=\"700\">%s<tspan dx=\"9\" fill=\"%s\" font-size=\"14\" font-weight=\"500\">stars</tspan></text>\n", L, ACCENT, comma(n), MUTED
	printf "<text x=\"%d\" y=\"36\" fill=\"%s\" font-size=\"13\" text-anchor=\"end\">%s</text>\n", L + W, MUTED, repo

	for (v = 0; v <= ymax + 0.5; v += step) {
		printf "<line x1=\"%d\" y1=\"%.1f\" x2=\"%d\" y2=\"%.1f\" stroke=\"%s\" stroke-opacity=\"%s\"/>\n", L, py(v), L + W, py(v), MUTED, (v == 0 ? "0.45" : "0.18")
		printf "<text x=\"%d\" y=\"%.1f\" fill=\"%s\" font-size=\"12\" text-anchor=\"end\">%s</text>\n", L - 12, py(v) + 4, MUTED, comma(v)
	}

	lasty = ""
	for (mo = m0; mo <= m1; mo += mstep) {
		ty = int(mo / 12); tm = mo % 12 + 1
		d = days_from_civil(ty, tm, 1)
		if (d < dmin || d > dmax) continue
		lbl = (ty == lasty) ? MON[tm] : MON[tm] " " ty
		lasty = ty
		printf "<text x=\"%.1f\" y=\"%d\" fill=\"%s\" font-size=\"12\" text-anchor=\"middle\">%s</text>\n", px(d), T + H + 26, MUTED, lbl
	}

	printf "<path d=\"%s\" fill=\"url(#fade)\"/>\n", area
	printf "<polyline points=\"%s\" fill=\"none\" stroke=\"%s\" stroke-width=\"2.25\" stroke-linecap=\"round\" stroke-linejoin=\"round\"/>\n", line, ACCENT
	printf "<circle cx=\"%.1f\" cy=\"%.1f\" r=\"7\" fill=\"%s\" fill-opacity=\"0.22\"/>\n", px(dmax), py(n), ACCENT
	printf "<circle cx=\"%.1f\" cy=\"%.1f\" r=\"3.25\" fill=\"%s\"/>\n", px(dmax), py(n), ACCENT
	print "</svg>"
}
AWK_EOF

awk -v repo="$REPO" -f "$WORK/chart.awk" "$WORK/sorted" > "$WORK/out.svg"

mkdir -p "$(dirname "$OUT")"
if [ -f "$OUT" ] && cmp -s "$WORK/out.svg" "$OUT"; then
  echo "star-history.sh: $OUT already matches ${REPO}'s current star count"
  exit 0
fi
cp "$WORK/out.svg" "$OUT"
echo "star-history.sh: wrote $OUT"
