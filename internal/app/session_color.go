package app

import (
	"image/color"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// A session's colour tells two sessions apart, so it is spent only where more
// than one of them is on screen at once: the rail's sessions section, the
// rail's agents section (which lists panes across every session), the session
// switcher, and the collapsed strip. The content area and the rail's terminals
// section show one session's panes and nothing else, so a colour there would
// distinguish them from nothing and is left off.
//
// The colour comes from the session's name, which is its identity everywhere
// else too. That makes it stable across a daemon restart, identical on every
// attached client with nothing stored and no round trip to agree on, and
// unchanged by a display-name rename. The price is collisions: six hues means
// two sessions can land on the same one. set-session-accent is the way out and
// always wins, which is what makes the collision an annoyance rather than a
// defect.

// The six chromatic bright ANSI slots, as legacy accent indices (0-7 are ANSI
// 8-15). Bright black and bright white are skipped: a session is identified by
// hue, and the two achromatic slots are the rail's own ink and its background.
const (
	sessionAccentSlotFirst = 1 // bright red
	sessionAccentSlotCount = 6 // through bright cyan
)

// sessionAccentNames maps the words set-session-accent takes to legacy accent
// slots. The daemon records the string verbatim and has never interpreted it,
// so this is the whole vocabulary; anything else reads as unset and the
// automatic colour stands.
var sessionAccentNames = map[string]int{
	"brightblack": 0, "brightred": 1, "brightgreen": 2, "brightyellow": 3,
	"brightblue": 4, "brightpurple": 5, "brightmagenta": 5, "brightcyan": 6,
	"brightwhite": 7,
	"black":       0, // ANSI 0 is unreachable as an accent, so plain black reads as the bright one
	"red":         8, "green": 9, "yellow": 10, "blue": 11,
	"purple": 12, "magenta": 12, "cyan": 13, "white": 14,
}

// ParseAccent reads the free-form string a session's accent is recorded as: a
// colour name from the ANSI sixteen, or a #rrggbb (or #rgb) literal. Names are
// matched loosely because the string is typed by a human at a CLI, so "Bright
// Blue", "bright-blue" and "brightblue" are one value.
func ParseAccent(s string) (Accent, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Accent{}, false
	}
	key := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '-', '_':
			return -1
		}
		return r
	}, strings.ToLower(s))
	if slot, ok := sessionAccentNames[key]; ok {
		return SlotAccent(slot), true
	}
	if c, ok := parseHexColor(s); ok {
		return RGBAccent(c), true
	}
	return Accent{}, false
}

// sessionAutoAccent is the colour a session with no explicit accent is known
// by: an FNV-1a fold of its name onto one of the six hues. Same name, same
// colour, on every client and after every restart.
func sessionAutoAccent(name string) Accent {
	const prime = 1099511628211
	h := uint64(1469598103934665603)
	for i := range len(name) {
		h ^= uint64(name[i])
		h *= prime
	}
	return SlotAccent(sessionAccentSlotFirst + int(h%sessionAccentSlotCount))
}

// sessionAccentString is the accent the daemon has recorded for a session, from
// the state push for the attached one and from the cached listing for the rest.
func (m *OS) sessionAccentString(name string) string {
	switch {
	case name == "":
		return ""
	case name == m.SessionName:
		return m.SessionAccent
	case m.DaemonClient != nil:
		_, accent := m.DaemonClient.SessionLabel(name)
		return accent
	}
	return ""
}

// SessionColor is the accent a session is known by, and whether it has one. The
// precedence is the whole contract: an accent the user set with
// set-session-accent wins outright, an unset or unreadable one falls back to
// the automatic colour rather than to nothing, and the config key off returns
// nothing at all so every surface renders as it did before.
func (m *OS) SessionColor(name string) (Accent, bool) {
	if !config.SessionColors || name == "" {
		return Accent{}, false
	}
	if a, ok := ParseAccent(m.sessionAccentString(name)); ok {
		return a, true
	}
	return sessionAutoAccent(name), true
}

// sessionTint is SessionColor lifted until it reads on the ground it is about
// to be drawn on, or nil when the session has no colour. Every automatic colour
// on screen goes through here: a hue from the theme's ANSI sixteen against a
// theme's own background is legible for some themes and a smudge on others, and
// which is which is not something to decide by eye.
func (m *OS) sessionTint(name string, bg color.Color) color.Color {
	a, ok := m.SessionColor(name)
	if !ok {
		return nil
	}
	return theme.Readable(a.RGB(), bg)
}

// railGround is what a rail row is actually drawn on: the band under the
// pointer or the cursor when it has one, and the terminal's own background
// otherwise, since the rail paints no slab of its own. Contrast is measured
// against this and never against the overlay palette's panel colour, which the
// rail never uses.
func railGround(rowBg color.Color) color.Color {
	if rowBg != nil {
		return rowBg
	}
	return theme.TerminalBg()
}

// agentIdentityTint is the colour an agents-section row is marked with. The
// section is the one place panes from several sessions stand in one list, so a
// row says which session it came from in the same column and the same colour
// the sessions section uses. An accent the user pinned to that pane outranks
// the session's colour: it is the more specific thing they asked for.
func (m *OS) agentIdentityTint(e sidebarAgentEntry, bg color.Color) color.Color {
	if !config.SessionColors {
		return nil
	}
	if a, ok := m.WindowAccent(e.WindowID); ok {
		return theme.Readable(a.RGB(), bg)
	}
	return m.sessionTint(e.SessionID, bg)
}
