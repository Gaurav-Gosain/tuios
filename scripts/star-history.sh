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
# The chart is drawn as a tuios pane: rounded accent border, a title pill on
# the border row, window controls on the other end, and a dock strip under the
# pane. The colors are the chrome palette theme.UI() resolves on the default
# charmtone ramp, not a lookalike, and the chrome is constant-dark for the
# same reason the real chrome is: it does not follow the ground it sits on,
# which is what lets one file serve both README themes. The curve itself is
# quantized to an 8x2 px cell grid, the way a terminal would have to draw it.
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

# Axis and milestone labels: thousands compress to "1k" so the gutter stays
# narrow; anything the step keeps below a thousand is written out.
function fmtk(v) {
	if (v >= 1000 && v % 1000 == 0) return (v / 1000) "k"
	return comma(v)
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
	L = 64; T = 96; W = 696; H = 234
	VW = 800; VH = 440
	# theme.UI() on the default charmtone ramp: Pepper canvas, BBQ dock, Char
	# surface, Charple accent with Hazy one step brighter, the
	# Butter/Smoke/Squid text ramp, Structure(Pepper) for gridlines, and Julep
	# because growth is what the success color is for. Quoted by value so the
	# chart cannot drift when a lookalike palette would have been close enough.
	CANVAS = "#201F26"; PANEL = "#2D2C36"; SURFACE = "#3A3943"
	ACCENT = "#6B50FF"; HAZY = "#8B75FF"
	FG = "#FFFAF1"; FGDIM = "#BFBCC8"; FGMUTE = "#858392"
	STRUCT = "#4D4B4F"; JULEP = "#00FFB2"
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

	R = L + W
	B = T + H
	step = nice_step(n / 4)
	ymax = step * (int(n / step) + 1)

	# The pane's border rows and the dock strip under it.
	ptop = 22
	pbot = VH - 44
	dy = VH - 34

	printf "<svg xmlns=\"http://www.w3.org/2000/svg\" viewBox=\"0 0 %d %d\" width=\"%d\" height=\"%d\" role=\"img\" aria-label=\"%s star history: %s stars\">\n", VW, VH, VW, VH, repo, comma(n)
	printf "<defs><linearGradient id=\"fade\" x1=\"0\" y1=\"0\" x2=\"0\" y2=\"1\">"
	printf "<stop offset=\"0\" stop-color=\"%s\" stop-opacity=\"0.30\"/>", ACCENT
	printf "<stop offset=\"1\" stop-color=\"%s\" stop-opacity=\"0.04\"/>", ACCENT
	printf "</linearGradient></defs>\n"
	# No webfont: an SVG in a README cannot fetch one, so the stack asks for
	# whatever monospace the reader's platform draws its terminals in.
	printf "<style>text{font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace}</style>\n"

	# The pane: canvas fill under a rounded accent stroke, the way the focused
	# window wears its border.
	printf "<rect x=\"8\" y=\"%d\" width=\"784\" height=\"%d\" rx=\"8\" fill=\"%s\"/>\n", ptop, pbot - ptop, CANVAS
	printf "<rect x=\"8\" y=\"%d\" width=\"784\" height=\"%d\" rx=\"8\" fill=\"none\" stroke=\"%s\" stroke-width=\"2\"/>\n", ptop, pbot - ptop, ACCENT
	# Title pill on the border row, half-circle ends like the powerline badge.
	printf "<rect x=\"30\" y=\"%d\" width=\"116\" height=\"22\" rx=\"11\" fill=\"%s\"/>\n", ptop - 11, ACCENT
	printf "<text x=\"88\" y=\"%d\" font-size=\"13\" font-weight=\"700\" fill=\"%s\" text-anchor=\"middle\">star-history</text>\n", ptop + 5, FG
	# Window controls on the end the badge is not.
	printf "<rect x=\"714\" y=\"%d\" width=\"56\" height=\"20\" rx=\"10\" fill=\"%s\"/>\n", ptop - 10, SURFACE
	printf "<text x=\"730\" y=\"%d\" font-size=\"12\" fill=\"%s\" text-anchor=\"middle\">&#9633;</text>\n", ptop + 4, FGDIM
	printf "<text x=\"754\" y=\"%d\" font-size=\"12\" fill=\"%s\" text-anchor=\"middle\">&#10005;</text>\n", ptop + 4, FGDIM

	printf "<text x=\"34\" y=\"80\" font-size=\"30\" font-weight=\"700\" fill=\"%s\">%s<tspan dx=\"10\" font-size=\"13\" font-weight=\"500\" fill=\"%s\">stars</tspan></text>\n", FG, comma(n), FGDIM

	# Gridlines dotted in structure ink: quiet by the same 1.9:1 target the
	# real chrome holds its rules to.
	for (v = step; v <= ymax + 0.5; v += step) {
		printf "<line x1=\"%d\" y1=\"%.1f\" x2=\"%d\" y2=\"%.1f\" stroke=\"%s\" stroke-dasharray=\"1 5\"/>\n", L, py(v), R, py(v), STRUCT
		printf "<text x=\"%d\" y=\"%.1f\" font-size=\"11\" fill=\"%s\" text-anchor=\"end\">%s</text>\n", L - 8, py(v) + 4, FGMUTE, fmtk(v)
	}

	# Ticks on month boundaries rather than at even pixel offsets: a reader
	# scanning the axis is looking for months, not for equal spacing.
	civil_from_days(int(dmin)); m0 = CY * 12 + CM - 1
	if (days_from_civil(CY, CM, 1) < dmin) m0++
	civil_from_days(int(dmax)); m1 = CY * 12 + CM - 1
	span = m1 - m0
	mstep = 1
	split("1 2 3 6 12", CAND, " ")
	for (i = 1; i <= 5; i++) { mstep = CAND[i]; if (span / mstep <= 6) break }
	lasty = ""
	for (mo = m0; mo <= m1; mo += mstep) {
		ty = int(mo / 12); tm = mo % 12 + 1
		d = days_from_civil(ty, tm, 1)
		if (d < dmin || d > dmax) continue
		lbl = (ty == lasty) ? MON[tm] : MON[tm] " " ty
		lasty = ty
		printf "<text x=\"%.1f\" y=\"%d\" font-size=\"11\" fill=\"%s\" text-anchor=\"middle\">%s</text>\n", px(d), B + 18, FGMUTE, lbl
	}

	# The curve as a terminal has to draw it: one 8px column per cell, height
	# snapped to 2px eighth-blocks. The stars are sorted, so each column's
	# count is one pointer walked forward, and the staircase keeps the cliffs
	# a smooth curve would round off.
	k = 0
	fill = sprintf("M %d %d", L, B)
	edge = ""
	for (x = L; x < R; x += 8) {
		xe = x + 8; if (xe > R) xe = R
		de = dmin + (xe - L) * (dmax - dmin) / W
		while (k < n && day[k + 1] <= de) k++
		yq = B - int((B - py(k)) / 2 + 0.5) * 2
		fill = fill sprintf(" L %d %d L %d %d", x, yq, xe, yq)
		if (edge == "") edge = sprintf("M %d %d", x, yq)
		else edge = edge sprintf(" L %d %d", x, yq)
		edge = edge sprintf(" L %d %d", xe, yq)
		tipy = yq
	}
	fill = fill sprintf(" L %d %d Z", R, B)
	printf "<path d=\"%s\" fill=\"url(#fade)\"/>\n", fill
	printf "<path d=\"%s\" fill=\"none\" stroke=\"%s\" stroke-width=\"2\"/>\n", edge, ACCENT

	# A milestone at each gridline the curve has crossed: the crossing's exact
	# point and date, marked with a four-point star because that is the mark
	# this chart is about.
	for (m = step; m <= n; m += step) {
		mx = px(day[m]); my = py(m)
		printf "<path d=\"M %.1f %.1f l 1.8 4.2 4.2 1.8 -4.2 1.8 -1.8 4.2 -1.8 -4.2 -4.2 -1.8 4.2 -1.8 z\" fill=\"%s\"/>\n", mx, my - 6, HAZY
		civil_from_days(int(day[m]))
		printf "<text x=\"%.1f\" y=\"%.1f\" font-size=\"11\" fill=\"%s\" text-anchor=\"end\">%s &#183; %s %02d</text>\n", mx - 9, my - 9, FGDIM, fmtk(m), MON[CM], CD
	}

	# The steepest 48 hours, found by two pointers over the sorted days. Only a
	# spike worth a tenth of the whole history gets the caption; a quiet repo's
	# best window is just its slope, and captioning it would be noise.
	best = 0; bi = 1; bj = 1
	j = 1
	for (i = 1; i <= n; i++) {
		while (day[i] - day[j] > 2) j++
		if (i - j + 1 > best) { best = i - j + 1; bi = i; bj = j }
	}
	if (best * 10 >= n && n >= 100) {
		mx = (px(day[bj]) + px(day[bi])) / 2
		my = (py(bj) + py(bi)) / 2
		printf "<line x1=\"%.0f\" y1=\"%.0f\" x2=\"%.0f\" y2=\"%.0f\" stroke=\"%s\" stroke-width=\"1\"/>\n", mx - 26, my + 34, mx - 4, my + 4, JULEP
		printf "<text x=\"%.0f\" y=\"%.0f\" font-size=\"12\" font-weight=\"700\" fill=\"%s\" text-anchor=\"end\">+%s in 48h</text>\n", mx - 20, my + 48, JULEP, comma(best)
	}

	# The cursor after the last cell, blinking, because the history is still
	# being written. SMIL rather than script: GitHub strips scripts but serves
	# the file as an image, where declarative animation survives.
	printf "<rect x=\"%d\" y=\"%d\" width=\"9\" height=\"16\" fill=\"%s\">", R + 3, tipy - 14, HAZY
	printf "<animate attributeName=\"opacity\" values=\"1;1;0;0;1\" keyTimes=\"0;0.5;0.5;0.95;1\" dur=\"1.2s\" repeatCount=\"indefinite\"/></rect>\n"

	# The dock: workspace pills, the repo where the clock would go, and the
	# mode pill saying the one thing a chart of a healthy repo should.
	printf "<rect x=\"8\" y=\"%d\" width=\"784\" height=\"26\" rx=\"6\" fill=\"%s\"/>\n", dy, PANEL
	printf "<rect x=\"20\" y=\"%d\" width=\"24\" height=\"18\" rx=\"4\" fill=\"%s\"/>\n", dy + 4, SURFACE
	printf "<text x=\"32\" y=\"%d\" font-size=\"12\" font-weight=\"700\" fill=\"%s\" text-anchor=\"middle\" text-decoration=\"underline\">1</text>\n", dy + 17, HAZY
	printf "<text x=\"62\" y=\"%d\" font-size=\"12\" fill=\"%s\" text-anchor=\"middle\">2</text>\n", dy + 17, FGDIM
	printf "<text x=\"92\" y=\"%d\" font-size=\"12\" fill=\"%s\" text-anchor=\"middle\">3</text>\n", dy + 17, FGDIM
	printf "<text x=\"560\" y=\"%d\" font-size=\"12\" fill=\"%s\" text-anchor=\"middle\">&#10022; %s</text>\n", dy + 17, FGDIM, repo
	printf "<rect x=\"690\" y=\"%d\" width=\"84\" height=\"18\" rx=\"9\" fill=\"%s\"/>\n", dy + 4, ACCENT
	printf "<text x=\"732\" y=\"%d\" font-size=\"11\" font-weight=\"700\" fill=\"%s\" text-anchor=\"middle\">NORMAL</text>\n", dy + 17, FG
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