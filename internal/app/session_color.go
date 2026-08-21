package app

import (
	"image/color"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// A session's colour tells two sessions apart, so it is spent only where more
// than one of them is on screen at once: the rail's sessions section, the
// rail's agents section (which lists panes across every session), the session
// switcher, and the collapsed strip. The content area and the rail's terminals
// section show one session's panes and nothing else, so a colour per row there
// would distinguish them from nothing and is left off.
//
// The focus marks are the exception, and they are about coherence rather than
// information: the rail is one object, so the bar on the focused pane is the
// same hue as the bar on the session two rows above it. A single mark taking a
// colour the rail is already showing adds no vocabulary; two marks contradicting
// each other is what read as a defect.
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

// sessionPreferredSlot is the hue a session asks for: an FNV-1a fold of its
// name, so the same name asks for the same hue on every client and after every
// restart, with nothing written down.
func sessionPreferredSlot(name string) int {
	const prime = 1099511628211
	h := uint64(1469598103934665603)
	for i := range len(name) {
		h ^= uint64(name[i])
		h *= prime
	}
	return int(h % sessionAccentSlotCount)
}

// sessionAutoAccent is the colour a session gets when nothing is known about
// what else exists: its preferred hue, unarbitrated.
func sessionAutoAccent(name string) Accent {
	return SlotAccent(sessionAccentSlotFirst + sessionPreferredSlot(name))
}

// assignSessionColors hands out a hue to each name, settling the collisions six
// hues make unavoidable. Three sessions asking at random collide about half the
// time, and two sessions in one colour is the exact case the colours exist to
// prevent, so the ask alone is not enough.
//
// Everyone who can have the hue they asked for gets it, in sorted-name order so
// the answer depends on the set of sessions and on nothing local: not on the
// rail's drag order, not on which session this client is attached to. Whoever is
// left walks forward to the first free hue. A session nobody else asked for
// therefore keeps its colour for as long as it exists, and only a session that
// was already in a collision can be moved by one appearing or going away.
//
// reserved holds the hues explicit accents have already claimed, so an accent
// the user set is not duplicated by one we derived.
func assignSessionColors(names []string, reserved [sessionAccentSlotCount]bool) map[string]Accent {
	sorted := slices.Clone(names)
	slices.Sort(sorted)

	out := make(map[string]Accent, len(sorted))
	taken := reserved
	var spilled []string
	for _, name := range sorted {
		if _, done := out[name]; done || name == "" {
			continue
		}
		if slot := sessionPreferredSlot(name); !taken[slot] {
			taken[slot] = true
			out[name] = SlotAccent(sessionAccentSlotFirst + slot)
			continue
		}
		spilled = append(spilled, name)
	}
	for _, name := range spilled {
		slot := sessionPreferredSlot(name)
		for step := 1; step <= sessionAccentSlotCount; step++ {
			next := (slot + step) % sessionAccentSlotCount
			if !taken[next] {
				slot = next
				break
			}
		}
		// Past the sixth session there is no free hue left and the preferred one
		// stands: a duplicate is better than a hue picked by arithmetic nobody
		// can predict.
		taken[slot] = true
		out[name] = SlotAccent(sessionAccentSlotFirst + slot)
	}
	return out
}

// refreshSessionColorsFor is refreshSessionColors over the session nodes a
// surface was handed, which is the form both callers have.
func (m *OS) refreshSessionColorsFor(sessions []sessiontree.Node) {
	names := make([]string, 0, len(sessions))
	for i := range sessions {
		names = append(names, sessions[i].ID)
	}
	m.refreshSessionColors(names)
}

// refreshSessionColors settles the colours for the sessions a surface is about
// to draw. Called once per rail and once per switcher render, off the cached
// path, so a row can ask per cell without redoing the arbitration.
func (m *OS) refreshSessionColors(names []string) {
	if !config.SessionColors {
		m.sessionColors = nil
		return
	}
	var reserved [sessionAccentSlotCount]bool
	auto := names[:0:0]
	for _, name := range names {
		a, ok := ParseAccent(m.sessionAccentString(name))
		if !ok {
			auto = append(auto, name)
			continue
		}
		if slot, ok := sessionReservedSlot(a); ok {
			reserved[slot] = true
		}
	}
	m.sessionColors = assignSessionColors(auto, reserved)
}

// sessionReservedSlot is the hue an explicit accent takes out of the automatic
// pool. A named accent says its slot outright. A literal claims one only when it
// is exactly that slot's colour, which is what the picker writes when the user
// lands on the session's own hue: without the match, an accent set to the very
// colour another session was about to be handed would not stop it being handed
// out, and the two would collide in the one way the colours exist to prevent.
func sessionReservedSlot(a Accent) (int, bool) {
	if a.IsSlot() {
		slot := a.Slot - sessionAccentSlotFirst
		return slot, slot >= 0 && slot < sessionAccentSlotCount
	}
	rgb := a.RGB()
	for i := range sessionAccentSlotCount {
		if SlotAccent(sessionAccentSlotFirst+i).RGB() == rgb {
			return i, true
		}
	}
	return 0, false
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
//
// The automatic colour is the arbitrated one when the surface being drawn has
// said which sessions it holds, and the session's bare preference otherwise, so
// a caller outside a render still gets a stable answer rather than none.
func (m *OS) SessionColor(name string) (Accent, bool) {
	if !config.SessionColors || name == "" {
		return Accent{}, false
	}
	// An open picker outranks all of it, so every surface wearing this session's
	// colour shows the one under the cursor while it is being chosen.
	if a, ok := m.accentPreview(AccentTargetSession, name); ok {
		return a, true
	}
	if a, ok := ParseAccent(m.sessionAccentString(name)); ok {
		return a, true
	}
	if a, ok := m.sessionColors[name]; ok {
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

// accentSource says where the colour something is wearing came from, which the
// colour itself cannot: a derived colour and a pinned one are the same pixels
// and follow different rules.
type accentSource uint8

const (
	accentSourceNone accentSource = iota
	// accentSourceOwn is a colour the user set on this exact thing.
	accentSourceOwn
	// accentSourceSession is a pane wearing the colour of the session it is in.
	accentSourceSession
	// accentSourceAuto is a session wearing the colour it was assigned.
	accentSourceAuto
	// accentSourceDefault is a setting showing the colour it falls back to
	// because nothing is set on it. The colour is real and on screen; what the
	// picker must not do is write it back as if the user had chosen it.
	accentSourceDefault
)

// derived reports whether the colour came from somewhere other than the thing
// itself, which is the case the readout names in words rather than in hex.
func (s accentSource) derived() bool {
	return s == accentSourceSession || s == accentSourceAuto || s == accentSourceDefault
}

// effectiveAccent is the colour a pane is actually wearing: the accent pinned
// to it when it has one, and its session's colour otherwise. This is the whole
// precedence in one place, so the picker opens on the colour the rail is
// drawing rather than on a second opinion about what that colour is.
func (m *OS) effectiveAccent(windowID, sessionID string) (Accent, accentSource) {
	if a, ok := m.WindowAccent(windowID); ok {
		return a, accentSourceOwn
	}
	if a, ok := m.SessionColor(sessionID); ok {
		return a, accentSourceSession
	}
	return Accent{}, accentSourceNone
}

// sessionEffectiveAccent is the same question one level up: the accent the
// session was given, or the one it was assigned. It reads the daemon's recorded
// string through the same parser SessionColor uses, so the two cannot disagree
// about what "cyan" means.
func (m *OS) sessionEffectiveAccent(name string) (Accent, accentSource) {
	if name == "" {
		return Accent{}, accentSourceNone
	}
	if a, ok := ParseAccent(m.sessionAccentString(name)); ok {
		return a, accentSourceOwn
	}
	if a, ok := m.SessionColor(name); ok {
		return a, accentSourceAuto
	}
	return Accent{}, accentSourceNone
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
	a, src := m.effectiveAccent(e.WindowID, e.SessionID)
	if src == accentSourceNone {
		return nil
	}
	return theme.Readable(a.RGB(), bg)
}
