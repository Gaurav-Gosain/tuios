package app

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// Whether the host terminal can draw kitty Unicode placeholders.
//
// There is no way to ask it. The graphics protocol has a query action, but it
// answers for graphics as a whole and kitty's documentation offers nothing
// finer; the capability kitten's whole list is names, fonts, colours and the
// operating system, with nothing about images in it. The obvious probe does not
// work either: the specification says a virtual placement must carry c and r,
// so a terminal that implements placeholders ought to refuse one without them,
// but Ghostty answers OK to exactly that while supporting the feature, which
// makes a refusal-based probe a false negative. Every other client library
// reaches the same conclusion and guesses from the environment.
//
// So this asks the terminal who it is and looks the answer up. That is a
// heuristic, but it is a better one than reading TERM: XTVERSION is answered by
// the terminal on the other end of this tty, now, while TERM and TERM_PROGRAM
// are inherited environment variables that go stale and that a multiplexer
// rewrites.
//
// What keeps the heuristic honest is the direction it fails in. Placeholders
// are used only on a positive match, and everything else falls back to the
// placement path, which works on every kitty-graphics host. A table that is
// wrong or out of date therefore costs the nicer behaviour and never the
// picture.

// placeholderMinimum is the first version of each terminal that draws Unicode
// placeholders. A name that is not here is assumed not to.
var placeholderMinimum = map[string][3]int{
	"kitty":   {0, 28, 0}, // added in 0.28.0
	"ghostty": {1, 0, 0},  // ghostty PR 2015, shipped in 1.0.0
	"wezterm": {0, 0, 0},  // dates from its own placeholder support
	"rio":     {0, 0, 0},
}

// xtversionReply matches the DCS a terminal answers CSI > q with, which is
// "ESC P > | <name> <version> ESC \".
var xtversionReply = regexp.MustCompile(`\x1bP>\|([^\x1b]*)\x1b\\`)

// xtversionQuery is the request itself. It rides in the capability probe's
// existing round trip, so it costs no extra latency.
const xtversionQuery = "\x1b[>q"

// parseHostIdentity reads the terminal's name and version out of a probe
// response. The name is lowercased and the version is whatever digits follow
// it; both are empty when the terminal did not answer.
func parseHostIdentity(response string) (name string, version [3]int, ok bool) {
	m := xtversionReply.FindStringSubmatch(response)
	if m == nil {
		return "", version, false
	}
	// Two spellings in the wild: "ghostty 1.3.1" and "kitty(0.32.2)".
	s := strings.TrimSpace(m[1])
	s = strings.ReplaceAll(s, "(", " ")
	s = strings.TrimSuffix(s, ")")
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return "", version, false
	}
	name = strings.ToLower(fields[0])
	if len(fields) > 1 {
		version = parseVersionTriple(fields[1])
	}
	return name, version, true
}

func parseVersionTriple(s string) [3]int {
	var out [3]int
	for i, part := range strings.SplitN(s, ".", 3) {
		n := 0
		for _, r := range part {
			if r < '0' || r > '9' {
				break
			}
			n = n*10 + int(r-'0')
		}
		if v, err := strconv.Atoi(strings.TrimSpace(part)); err == nil {
			n = v
		}
		out[i] = n
	}
	return out
}

// hostDrawsPlaceholders reports whether the terminal that answered this probe
// draws Unicode placeholders.
func hostDrawsPlaceholders(response string) bool {
	name, version, ok := parseHostIdentity(response)
	if !ok {
		return false
	}
	minimum, known := placeholderMinimum[name]
	if !known {
		return false
	}
	return !versionLess(version, minimum)
}

func versionLess(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// placeholdersEnabled resolves the setting against what the host can do.
//
// "on" and "off" are answers, and "auto" asks the terminal. The passthrough
// being off at all means no image reaches the host, so nothing can draw a
// placeholder either.
func (m *OS) placeholdersEnabled() bool {
	if m.KittyPassthrough == nil || !m.KittyPassthrough.IsEnabled() {
		return false
	}
	switch m.Settings.KittyPlaceholders {
	case config.KittyPlaceholdersOn:
		return true
	case config.KittyPlaceholdersOff:
		return false
	default:
		return m.hostCaps().KittyPlaceholders
	}
}
