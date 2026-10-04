package guestenv

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// FallbackTerm is the TERM a pane gets when the one it was handed has no
// terminfo entry on the machine the pane runs on.
//
// xterm-256color is the entry most likely to be installed: Debian and Ubuntu
// ship it in ncurses-base, which every install has, Fedora and Arch in their
// base ncurses package, Alpine in ncurses-terminfo-base, and macOS and the
// BSDs in the base system. tmux-256color would describe tuios no better and is
// missing from all of those minimal sets (Debian keeps it in ncurses-term, and
// the ncurses 5.7 macOS ships predates it). It is also what the daemon already
// gives a session whose client named no TERM.
const FallbackTerm = "xterm-256color"

// PaneTerm returns the TERM to give a process in a pane on this machine:
// term itself when this machine's terminfo database has an entry for it, and
// FallbackTerm when it does not.
//
// The TERM a pane is handed comes from the client that made the session, and
// that client's terminal is often one this machine knows nothing about
// (xterm-kitty, xterm-ghostty, alacritty, wezterm). Passed through as it is,
// clear, tput and every curses program in the pane fail with "unknown
// terminal". tmux and screen set their own TERM for the same reason.
//
// The lookup only stats files, so it is cheap enough to run for every pane, and
// it is run every time, so an entry installed while the daemon runs is picked
// up by the next pane. Windows has no terminfo, so term is kept there.
func PaneTerm(term string) string {
	switch {
	case term == "":
		return FallbackTerm
	case term == FallbackTerm, term == "dumb":
		// dumb is what a terminal that asked for no capabilities gets on
		// purpose, and every terminfo database has it.
		return term
	case runtime.GOOS == "windows":
		return term
	}
	if hasTerminfo(term, os.Getenv, os.UserHomeDir) {
		return term
	}
	return FallbackTerm
}

// hasTerminfo reports whether a compiled terminfo entry named term is in one
// of the directories ncurses searches, read from getenv and home.
//
// The search follows ncurses: $TERMINFO, ~/.terminfo, each directory in
// $TERMINFO_DIRS (an empty element there means the system directory), and then
// the system directories distributions compile in. Each directory is tried in
// both layouts ncurses writes: a first-letter directory (x/xterm-kitty) and
// the hexadecimal one case-insensitive filesystems need (78/xterm-kitty).
//
// The hashed database some BSDs use (terminfo.db) is not read. A host that has
// only that answers false, and the pane gets FallbackTerm, which those systems
// carry. That errs toward a TERM that works.
func hasTerminfo(term string, getenv func(string) string, home func() (string, error)) bool {
	if !validTermName(term) {
		return false
	}
	for _, dir := range terminfoDirs(getenv, home) {
		if terminfoEntryIn(dir, term) {
			return true
		}
	}
	return false
}

// systemTerminfoDirs are the directories distributions compile ncurses to
// search: Debian and its derivatives /etc, /lib and /usr/share, Fedora, Arch
// and macOS /usr/share, some older systems /usr/lib, Solaris /usr/share/lib,
// and the FreeBSD ports ncurses /usr/local/share.
var systemTerminfoDirs = []string{
	"/etc/terminfo",
	"/lib/terminfo",
	"/usr/share/terminfo",
	"/usr/lib/terminfo",
	"/usr/share/lib/terminfo",
	"/usr/local/share/terminfo",
}

// terminfoDirs lists the directories hasTerminfo searches, in ncurses order.
func terminfoDirs(getenv func(string) string, home func() (string, error)) []string {
	var dirs []string
	if d := getenv("TERMINFO"); d != "" {
		dirs = append(dirs, d)
	}
	if h, err := home(); err == nil && h != "" {
		dirs = append(dirs, filepath.Join(h, ".terminfo"))
	}
	if list := getenv("TERMINFO_DIRS"); list != "" {
		for _, d := range filepath.SplitList(list) {
			if d == "" {
				d = "/usr/share/terminfo"
			}
			dirs = append(dirs, d)
		}
	}
	return append(dirs, systemTerminfoDirs...)
}

// terminfoEntryIn reports whether dir holds a compiled entry for term in either
// layout.
func terminfoEntryIn(dir, term string) bool {
	first := term[0]
	for _, sub := range []string{string(first), strconv.FormatUint(uint64(first), 16)} {
		info, err := os.Stat(filepath.Join(dir, sub, term))
		if err == nil && info.Mode().IsRegular() {
			return true
		}
	}
	return false
}

// validTermName reports whether term can name a terminfo entry. A name with a
// path separator, a leading dot or a control character is refused, so the
// value a client sent can never make the lookup stat a path outside the
// terminfo directories.
func validTermName(term string) bool {
	if term == "" || len(term) > 255 || term[0] == '.' {
		return false
	}
	if strings.ContainsAny(term, `/\`) {
		return false
	}
	for i := 0; i < len(term); i++ {
		if term[i] <= ' ' || term[i] == 0x7f {
			return false
		}
	}
	return true
}
