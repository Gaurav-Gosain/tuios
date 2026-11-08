#!/bin/sh
# ovnfs for tuios. One script: the first argument says what to do.
#
#   new               action: open a checkout of the focused pane's folder in a new pane
#   review            action: open the list of checkouts
#   checkouts         pane: the list, with diff, apply, remove and prune
#   diff, apply, apply-branch, attach [NAME]
#                     panes: act on one checkout
#   pane-closed       hook: a pane this plugin opened closed with changes, so say so
#   worktree-removed  hook: remove the checkouts of the removed worktree
#
# Nothing here applies changes without a yes typed in a pane. The hooks only
# tell the person and keep the work in a snapshot.

set -u
cmd=${1:-}
[ $# -gt 0 ] && shift

INSTALL="go install github.com/stencil-hq/ovnfs@latest"
STATE=${TUIOS_PLUGIN_STATE_DIR:-${TMPDIR:-/tmp}}
PANES="$STATE/panes"     # lines of "PANE NAME": the panes this plugin opened
PENDING="$STATE/pending" # lines of "NAME": closed panes that left changes

notify() {
	"$TUIOS_BIN" plugin-notify "$2" --level "$1" >/dev/null 2>&1 || :
}

# pause waits for Enter, so the person can read what a pane printed.
pause() {
	printf '\nPress Enter to close.'
	read -r _ || :
}

is_hook() {
	case $cmd in pane-closed | worktree-removed) return 0 ;; esac
	return 1
}

# need stops when ovnfs or jq is not on PATH, and says how to get them. A
# hook stops without a word: it did not ask the person for anything.
need() {
	missing=""
	command -v ovnfs >/dev/null 2>&1 || missing="ovnfs"
	command -v jq >/dev/null 2>&1 || missing="${missing:+$missing and }jq"
	[ -z "$missing" ] && return 0
	is_hook && exit 0
	msg="This plugin needs $missing on PATH."
	case $missing in *ovnfs*) msg="$msg Install ovnfs with: $INSTALL" ;; esac
	case $missing in *jq*) msg="$msg Install jq from your package manager." ;; esac
	if [ -t 1 ]; then
		printf '%s\n' "$msg"
		pause
	else
		notify warning "$msg"
	fi
	exit 0
}

ov() { ovnfs "$@"; }
ovj() { ovnfs --json "$@"; }

mountdir() { ovj start 2>/dev/null | jq -r '.mount // empty'; }

# checkout_at DIR prints the name of the checkout DIR is in, if any.
checkout_at() {
	m=$(mountdir)
	[ -n "$m" ] || return 0
	case "$1/" in
	"$m"/*/*)
		rest=${1#"$m"/}
		printf '%s\n' "${rest%%/*}"
		;;
	esac
}

# rows prints one checkout per line: name, changed (yes or no), idle, source.
# The jq program reads ovnfs ls --json.
# shellcheck disable=SC2016 # a jq program, not a shell string
ROWS='.checkouts[] | [.name, (if .dirty then "yes" else "no" end),
	((now - ((.touched // .created) | sub("\\.[0-9]+"; "") | fromdateiso8601)) as $s
		| if $s < 3600 then "\($s / 60 | floor)m"
		elif $s < 86400 then "\($s / 3600 | floor)h"
		else "\($s / 86400 | floor)d" end),
	.source] | @tsv'

rows() { ovj ls "$@" 2>/dev/null | jq -r "$ROWS"; }

exists() { ovj ls 2>/dev/null | jq -e --arg n "$1" 'any(.checkouts[]; .name == $n)' >/dev/null; }
dirty() { ovj ls 2>/dev/null | jq -e --arg n "$1" 'any(.checkouts[]; .name == $n and .dirty)' >/dev/null; }
source_of() { ovj ls 2>/dev/null | jq -r --arg n "$1" '.checkouts[] | select(.name == $n) | .source'; }
path_of() { ovj ls 2>/dev/null | jq -r --arg n "$1" '.checkouts[] | select(.name == $n) | .path'; }

# summary NAME prints the diff in one line, such as "2 changed, 1 added".
summary() {
	ovj diff "$1" 2>/dev/null | jq -r '
		[.changes[]?.status] as $s
		| [($s | map(select(. == "M")) | length | select(. > 0) | "\(.) changed"),
		   ($s | map(select(. == "A")) | length | select(. > 0) | "\(.) added"),
		   ($s | map(select(. == "D")) | length | select(. > 0) | "\(.) deleted"),
		   ($s | map(select(. != "M" and . != "A" and . != "D")) | length | select(. > 0) | "\(.) other")]
		| if length == 0 then "no changes" else join(", ") end'
}

# tilde shows a path under the home folder with a ~, for people to read.
tilde() {
	# shellcheck disable=SC2088 # the ~ is text, not a path
	case $1 in "$HOME"/*) printf '~/%s\n' "${1#"$HOME"/}" ;; *) printf '%s\n' "$1" ;; esac
}

# table prints the checkouts as numbered lines and keeps their names in
# $names, one per line.
table() {
	names=""
	n=0
	printf '   %-24s %-30s %-6s %s\n' CHECKOUT CHANGES IDLE FOLDER
	tab=$(printf '\t')
	while IFS="$tab" read -r name _changed idle src; do
		[ -n "$name" ] || continue
		n=$((n + 1))
		names="$names$name
"
		printf '%2d %-24s %-30s %-6s %s\n' "$n" "$name" "$(summary "$name")" "$idle" "$(tilde "$src")"
	done <<EOF
$(rows "$@")
EOF
	[ "$n" -gt 0 ] || printf '   No checkouts.\n'
}

nth() { printf '%s' "$names" | sed -n "${1}p"; }

# pick sets $target: the argument, else the checkout the focused pane is in,
# else the one checkout of the focused folder, else the one the person types.
pick() {
	target=${1:-}
	[ -n "$target" ] && return 0
	cwd=${TUIOS_PLUGIN_CWD:-}
	if [ -n "$cwd" ]; then
		target=$(checkout_at "$cwd")
		[ -n "$target" ] && return 0
		mine=$(rows "$cwd" | cut -f1)
		if [ -n "$mine" ] && [ "$(printf '%s\n' "$mine" | wc -l)" -eq 1 ]; then
			target=$mine
			return 0
		fi
	fi
	if [ -n "$cwd" ] && [ -n "${mine:-}" ]; then
		table "$cwd"
	else
		table
	fi
	[ -n "$names" ] || { pause; exit 0; }
	printf '\nType the number of a checkout and press Enter: '
	read -r choice || exit 0
	case $choice in '' | *[!0-9]*) exit 0 ;; esac
	target=$(nth "$choice")
	[ -n "$target" ] || exit 0
}

remember_pane() { printf '%s %s\n' "$1" "$2" >>"$PANES"; }

# set_cell shows the number of closed checkouts that still have changes.
set_cell() {
	[ -f "$PENDING" ] || { "$TUIOS_BIN" plugin-cell clear pending >/dev/null 2>&1; return 0; }
	keep=""
	while read -r name; do
		[ -n "$name" ] || continue
		dirty "$name" && keep="$keep$name
"
	done <"$PENDING"
	printf '%s' "$keep" | sort -u >"$PENDING.new" && mv "$PENDING.new" "$PENDING"
	count=$(grep -c . "$PENDING" 2>/dev/null)
	if [ "${count:-0}" -gt 0 ]; then
		"$TUIOS_BIN" plugin-cell set pending "ovnfs $count to review" --action review >/dev/null 2>&1
	else
		rm -f "$PENDING"
		"$TUIOS_BIN" plugin-cell clear pending >/dev/null 2>&1
	fi
	:
}

# keep_and_remove NAME WHY snapshots a checkout that has changes, then
# removes it. The snapshot keeps the work: ovnfs new --from SNAP opens it again.
keep_and_remove() {
	snap=""
	# dirty is a change since the last snapshot. An apply takes a snapshot
	# too, so a clean checkout can still differ from its base: its last
	# snapshot holds that, and rm keeps the snapshots.
	before=$(summary "$1")
	if dirty "$1"; then
		snap=$(ovj snap "$1" -m "$2" 2>/dev/null | jq -r '.snapshot.id // .id // empty')
		if [ -z "$snap" ]; then
			echo "The snapshot of $1 failed, so it was not removed."
			return 1
		fi
	fi
	ov rm "$1" >/dev/null 2>&1 || { echo "ovnfs could not remove $1."; return 1; }
	if [ -n "$snap" ]; then
		echo "Removed checkout $1. Its changes are in snapshot $snap: ovnfs new --from $snap opens them again."
	elif [ "$before" != "no changes" ]; then
		echo "Removed checkout $1. Its changes are in its last snapshot: ovnfs ls lists it."
	else
		echo "Removed checkout $1. It had no changes."
	fi
}

# enter NAME DIR records this pane for the pane.closed hook and runs the
# person's shell in DIR. The pane keeps the title tuios gave it: renaming a
# window needs the admin grant.
enter() {
	if [ -n "${TUIOS_PANE_ID:-}" ]; then
		remember_pane "$TUIOS_PANE_ID" "$1"
	fi
	cd "$2" || exit 1
	exec "${SHELL:-/bin/sh}"
}

need
mkdir -p "$STATE"

case $cmd in
new)
	cwd=${TUIOS_PLUGIN_CWD:-}
	if [ -z "$cwd" ] || [ ! -d "$cwd" ]; then
		printf 'The focused pane has no folder. Focus a pane in the folder you want a checkout of.\n'
		pause
		exit 0
	fi
	# stdout is the reply. The summary and any error go to stderr.
	if ! out=$(cd "$cwd" && ovj new . 2>"$STATE/new.err"); then
		printf 'ovnfs could not open a checkout of %s:\n' "$(tilde "$cwd")"
		cat "$STATE/new.err"
		pause
		exit 0
	fi
	name=$(printf '%s' "$out" | jq -r '.checkout.name // empty')
	path=$(printf '%s' "$out" | jq -r '.checkout.path // empty')
	if [ -z "$name" ] || [ -z "$path" ]; then
		printf 'ovnfs opened a checkout but did not say where. Run ovnfs ls to see it.\n'
		pause
		exit 0
	fi
	# The shell starts in the checkout's folder that matches the focused one.
	src=$(source_of "$name")
	dir=$path
	case "$cwd/" in "$src"/?*) [ -d "$path/${cwd#"$src"/}" ] && dir="$path/${cwd#"$src"/}" ;; esac
	printf 'This pane works in checkout %s.\nNothing goes into %s until you apply it: open ovnfs: Apply.\n\n' "$name" "$(tilde "$src")"
	enter "$name" "$dir"
	;;

shell)
	[ -f "$STATE/open" ] || exit 0
	name=$(cat "$STATE/open")
	rm -f "$STATE/open"
	exists "$name" || { printf 'There is no checkout %s.\n' "$name"; pause; exit 0; }
	enter "$name" "$(path_of "$name")"
	;;

review)
	"$TUIOS_BIN" plugins open tuios.ovnfs checkouts >/dev/null 2>&1 ||
		notify error "tuios could not open the list of checkouts."
	;;

checkouts)
	while :; do
		clear
		printf 'ovnfs checkouts\n\n'
		table
		printf '\nType a number and press Enter to choose a checkout.\n'
		printf 'p: prune.  r: refresh.  q: close.\n> '
		read -r choice || exit 0
		case $choice in
		q | Q) exit 0 ;;
		r | R | '') continue ;;
		p | P)
			printf 'Remove checkouts idle for how long? (such as 7d, 12h, 90m, Enter for 7d): '
			read -r idle || exit 0
			idle=${idle:-7d}
			printf 'Only the checkouts with no changes? Type n to include changed ones (Enter is yes): '
			read -r only || exit 0
			flags="--idle $idle"
			[ "$only" = n ] || flags="$flags --no-diff"
			printf '\nThese checkouts match:\n'
			# shellcheck disable=SC2086
			table $flags
			[ -n "$names" ] || { pause; continue; }
			printf '\nType yes and press Enter to remove them. Their snapshots stay: '
			read -r yes || exit 0
			if [ "$yes" = yes ]; then
				# shellcheck disable=SC2086
				ov prune $flags
				set_cell
				pause
			fi
			;;
		*[!0-9]*) ;;
		*)
			name=$(nth "$choice")
			[ -n "$name" ] || continue
			printf '\n%s: %s\n' "$name" "$(summary "$name")"
			printf 'd: diff.  a: apply.  b: apply to a branch.  o: open a pane in it.\n'
			printf 'h: attach to a host.  x: remove.  Enter: back.\n> '
			read -r act || exit 0
			case $act in
			d) sh "$0" diff "$name" ;;
			a) sh "$0" apply "$name" ;;
			b) sh "$0" apply-branch "$name" ;;
			h) sh "$0" attach "$name" ;;
			o)
				printf '%s\n' "$name" >"$STATE/open"
				"$TUIOS_BIN" plugins open tuios.ovnfs shell >/dev/null 2>&1 && exit 0
				printf 'tuios could not open a pane.\n'
				pause
				;;
			x)
				if dirty "$name"; then
					printf '%s has changes. Type yes and press Enter to keep them in a snapshot and remove the checkout: ' "$name"
				else
					printf 'Type yes and press Enter to remove %s: ' "$name"
				fi
				read -r yes || exit 0
				if [ "$yes" = yes ]; then
					keep_and_remove "$name" "removed from tuios"
					set_cell
					pause
				fi
				;;
			esac
			;;
		esac
	done
	;;

diff)
	pick "${1:-}"
	if [ "$(summary "$target")" = "no changes" ]; then
		printf '%s has no changes.\n' "$target"
		pause
		exit 0
	fi
	if command -v less >/dev/null 2>&1; then
		{ printf 'ovnfs diff %s (%s)\n\n' "$target" "$(summary "$target")"; ov diff "$target" -p 2>&1; } | less -R
	else
		ov diff "$target" -p 2>&1
		pause
	fi
	;;

apply)
	pick "${1:-}"
	src=$(source_of "$target")
	printf 'Apply %s to %s\n\n' "$target" "$(tilde "$src")"
	ov apply "$target" --dry-run 2>&1
	printf '\nType yes and press Enter to apply these changes: '
	read -r yes || exit 0
	if [ "$yes" != yes ]; then
		printf 'Nothing was applied.\n'
		pause
		exit 0
	fi
	if ov apply "$target" 2>&1; then
		printf '\nThe changes are in %s as uncommitted edits.\n' "$(tilde "$src")"
		# The checkout's changes are in the folder now, so it waits for no review.
		if [ -f "$PENDING" ]; then
			grep -vxF "$target" "$PENDING" >"$PENDING.new"
			mv "$PENDING.new" "$PENDING"
		fi
		set_cell
	else
		printf '\nSome files have conflicts. Resolve the markers in %s, then apply again.\n' "$(tilde "$src")"
	fi
	pause
	;;

apply-branch)
	pick "${1:-}"
	src=$(source_of "$target")
	printf 'Make a branch in %s from the commits of %s.\n' "$(tilde "$src")" "$target"
	printf 'Uncommitted edits in the checkout do not go to the branch.\n\n'
	printf 'Branch name (Enter for ovnfs/%s): ' "$target"
	read -r branch || exit 0
	branch=${branch:-ovnfs/$target}
	ov apply "$target" --branch "$branch" --dry-run 2>&1
	printf '\nType yes and press Enter to make the branch %s: ' "$branch"
	read -r yes || exit 0
	if [ "$yes" = yes ]; then
		ov apply "$target" --branch "$branch" 2>&1
	else
		printf 'Nothing was changed.\n'
	fi
	pause
	;;

attach)
	pick "${1:-}"
	printf 'Attach %s to another machine. Its files go there when a program reads them.\n\n' "$target"
	hosts=$("$TUIOS_BIN" hosts --json 2>/dev/null | jq -r '.hosts[]? | select(.status == "up") | "\(.host)\t\(.addr)"')
	if [ -n "$hosts" ]; then
		printf 'Hosts of tuios that are up:\n'
		printf '%s\n' "$hosts" | cut -f1 | sed 's/^/  /'
		printf '\n'
	fi
	printf 'Host (a tuios host, an ssh host or an ovnfs mesh member): '
	read -r host || exit 0
	[ -n "$host" ] || exit 0
	tab=$(printf '\t')
	addr=$(printf '%s\n' "$hosts" | awk -F"$tab" -v h="$host" '$1 == h { print $2 }')
	to=${addr:-$host}
	if ! out=$(ovj attach "$target" --to "$to" 2>&1); then
		printf '%s\n' "$out"
		pause
		exit 0
	fi
	rpath=$(printf '%s' "$out" | jq -r '.remote.path // .path // .checkout.path // empty' 2>/dev/null)
	printf 'Attached %s to %s%s.\n' "$target" "$host" "${rpath:+ at $rpath}"
	# A plugin opens no window on a host without the admin grant, so the
	# person runs the line.
	[ -n "$addr" ] && [ -n "$rpath" ] &&
		printf 'Open a pane there with: tuios new-window --host %s --cwd %s\n' "$host" "$rpath"
	printf 'Bring the edits made there back with: ovnfs pull %s\n' "$target"
	pause
	;;

pane-closed)
	[ -f "$PANES" ] || exit 0
	pane=$(printf '%s' "${TUIOS_PLUGIN_EVENT_JSON:-}" | jq -r '.data.pane_id // empty' 2>/dev/null)
	# A pane id is wSESSION:pWINDOW, each the first 12 hex digits of the id.
	short=${pane##*:p}
	[ -n "$short" ] || exit 0
	name=""
	keep=""
	while read -r id n; do
		[ -n "$id" ] || continue
		if [ "$(printf '%s' "$id" | tr -d - | cut -c1-12)" = "$short" ]; then
			name=$n
		else
			keep="$keep$id $n
"
		fi
	done <"$PANES"
	[ -n "$name" ] || exit 0
	printf '%s' "$keep" >"$PANES"
	exists "$name" || exit 0
	dirty "$name" || exit 0
	printf '%s\n' "$name" >>"$PENDING"
	notify info "The pane of checkout $name closed. Its changes are not in $(tilde "$(source_of "$name")") yet. Open ovnfs: Checkouts to see, apply or remove them."
	set_cell
	;;

worktree-removed)
	path=$(printf '%s' "${TUIOS_PLUGIN_EVENT_JSON:-}" | jq -r '.data.path // empty' 2>/dev/null)
	[ -n "$path" ] || exit 0
	# The folder is gone, so it cannot be resolved. On macOS one side can
	# name /var or /tmp through /private, so both drop that prefix.
	gone=$(ovj ls 2>/dev/null | jq -r --arg p "$path" '
		def norm: if test("^/private/(var|tmp|etc)/") then .[8:] else . end;
		($p | norm) as $p
		| .checkouts[] | (.source | norm) as $s
		| select($s == $p or ($s | startswith($p + "/"))) | .name')
	[ -n "$gone" ] || exit 0
	for name in $gone; do
		if out=$(keep_and_remove "$name" "worktree removed"); then
			notify info "$out Its worktree $(tilde "$path") was removed."
		else
			notify warning "$out Its worktree $(tilde "$path") was removed."
		fi
	done
	set_cell
	;;

*)
	echo "ovnfs.sh: unknown command '$cmd'" >&2
	exit 2
	;;
esac
