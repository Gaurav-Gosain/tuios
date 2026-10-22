#!/usr/bin/env bash
# binary-size-report.sh NOW_DIR MAIN_DIR [MAIN_LABEL]
#
# Prints a Markdown table of the binary sizes in NOW_DIR against those in
# MAIN_DIR, and the room left under the ceiling in scripts/binary-size.sh.
# Each directory holds files of "target bytes" lines, as binary-size.sh writes
# them with SIZE_OUT. MAIN_DIR may be empty or missing: the table then says
# that main has no measured size yet. MAIN_LABEL names main's measurement,
# such as a commit or a run link.
set -euo pipefail

now_dir=$1
main_dir=$2
main_label=${3:-main}

here=$(dirname "$0")
ceiling=$(sed -n 's/^ceiling=\([0-9][0-9]*\).*/\1/p' "$here/binary-size.sh")

lookup() { # dir target
	[ -d "$1" ] || return 0
	find "$1" -type f -exec cat {} + 2>/dev/null | awk -v t="$2" '$1 == t { v = $2 } END { if (v != "") print v }'
}

signed() { # bytes
	awk -v n="$1" 'BEGIN { s = n < 0 ? "-" : "+"; if (n < 0) n = -n; printf "%s%d bytes (%s%.1f KiB)", s, n, s, n / 1024 }'
}

targets=$(find "$now_dir" -type f -exec cat {} + 2>/dev/null | awk '{ print $1 }' | sort -u || true)
if [ -z "$targets" ]; then
	echo "No binary size was measured in this run."
	exit 0
fi

echo "| target | this change | main | change | room under the ceiling |"
echo "|---|---:|---:|---:|---:|"
for target in $targets; do
	now=$(lookup "$now_dir" "$target")
	base=$(lookup "$main_dir" "$target")
	if [ -n "$base" ]; then
		delta=$(signed $((now - base)))
		base_cell="$base bytes"
	else
		delta="not known"
		base_cell="not measured"
	fi
	echo "| $target | $now bytes | $base_cell | $delta | $((ceiling - now)) bytes |"
done
echo
echo "The ceiling is $ceiling bytes for each target. Main's size is from $main_label."
