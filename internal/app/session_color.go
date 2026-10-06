package app

import (
	"image/color"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
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
// unchanged by a display-name rename. The price is collisions: ten hues means
// two sessions can still land on the same one. set-session-accent is the way out and
// always wins, which is what makes the collision an annoyance rather than a
// defect.

// The ten chromatic ANSI slots a session colour is drawn from, as legacy accent
// indices (0-7 are ANSI 8-15, 8-14 are ANSI 1-7). Bright black and bright white
// are skipped: a session is identified by hue, and the two achromatic slots are
// the rail's own ink and its background. The six bright chromatic slots come
// first, each followed by its normal-ANSI twin where one exists; normal yellow
// is olive on the rail's ground and normal cyan sits too close to normal blue,
// so those two twins are left out and the two brights without a twin close the
// row.
const sessionAccentSlotCount = 10

var sessionAccentSlots = [sessionAccentSlotCount]int{
	1, 8, // bright red, red
	2, 9, // bright green, green
	4, 11, // bright blue, blue
	5, 12, // bright purple, purple
	3, // bright yellow
	6, // bright cyan
}

// sessionHue is one palette position a theme can tell apart, with the colour
// it shows on the rail's ground.
type sessionHue struct {
	slot  int
	shown color.Color
}

// sessionHueMinGap is how far apart two shown hues must sit in OKLab before a
// person can tell them apart in one cell. A pair closer than this is one hue.
const sessionHueMinGap = 0.05

// sessionAccentPool folds the candidate slots down to the hues the theme
// behind bg can tell apart. Many themes give a normal slot and its bright twin
// the same colour, or nearly one, and to a person reading one cell those are
// the same hue: a pool that kept both would hand two sessions colours that
// look equal, which is the exact thing the colours exist to prevent. A slot
// survives only when the colour it shows (lifted to read on the ground, then
// stepped down to what a 16-colour terminal paints) sits at least
// sessionHueMinGap from every hue already kept. A theme with distinct twins
// keeps all ten; one_dark keeps six.
func sessionAccentPool(bg color.Color) []sessionHue {
	pool := make([]sessionHue, 0, sessionAccentSlotCount)
	for _, idx := range sessionAccentSlots {
		shown := overlay.Shown(theme.Readable(SlotAccent(idx).RGB(), bg))
		crowded := false
		for _, kept := range pool {
			if overlay.Distance(shown, kept.shown) < sessionHueMinGap {
				crowded = true
				break
			}
		}
		if !crowded {
			pool = append(pool, sessionHue{slot: idx, shown: shown})
		}
	}
	return pool
}

// sessionPool is the pool for this client's ground, remembered so a render
// that asks per row does not re-lift the palette each time. A theme switch
// moves the ground, so the ground is the key.
func (m *OS) sessionPool() []sessionHue {
	bg := m.terminalBg()
	if m.sessionPoolCache != nil && m.sessionPoolBg == bg {
		return m.sessionPoolCache
	}
	m.sessionPoolCache = sessionAccentPool(bg)
	m.sessionPoolBg = bg
	return m.sessionPoolCache
}

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

// sessionPreferredSlot is the position in the pool a session asks for: an
// FNV-1a fold of its name, so the same name asks for the same hue on every
// client and after every restart, with nothing written down.
func sessionPreferredSlot(name string, size int) int {
	const prime = 1099511628211
	h := uint64(1469598103934665603)
	for i := range len(name) {
		h ^= uint64(name[i])
		h *= prime
	}
	return int(h % uint64(size))
}

// sessionAutoAccent is the colour a session gets when nothing is known about
// what else exists: its preferred hue, unarbitrated.
func sessionAutoAccent(name string, pool []sessionHue) Accent {
	return SlotAccent(pool[sessionPreferredSlot(name, len(pool))].slot)
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
func assignSessionColors(names []string, reserved []bool, pool []sessionHue) map[string]Accent {
	sorted := slices.Clone(names)
	slices.Sort(sorted)

	out := make(map[string]Accent, len(sorted))
	taken := reserved
	var spilled []string
	for _, name := range sorted {
		if _, done := out[name]; done || name == "" {
			continue
		}
		if slot := sessionPreferredSlot(name, len(pool)); !taken[slot] {
			taken[slot] = true
			out[name] = SlotAccent(pool[slot].slot)
			continue
		}
		spilled = append(spilled, name)
	}
	for _, name := range spilled {
		slot := sessionPreferredSlot(name, len(pool))
		for step := 1; step <= len(pool); step++ {
			next := (slot + step) % len(pool)
			if !taken[next] {
				slot = next
				break
			}
		}
		// Past the pool's size there is no free hue left and the preferred one
		// stands: a duplicate is better than a hue picked by arithmetic nobody
		// can predict.
		taken[slot] = true
		out[name] = SlotAccent(pool[slot].slot)
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
//
// The two callers arbitrate independently, and the pane borders read whichever
// ran last, so the switcher's render can leave the answer behind: past the
// pool's size a duplicate is repaired against the caller's own row order, so a
// session can change hue when the switcher opens. Up to the pool's size nobody
// shares and the two calls agree.
func (m *OS) refreshSessionColors(names []string) {
	if !m.Settings.SessionColors {
		m.sessionColors = nil
		return
	}
	pool := m.sessionPool()
	reserved := make([]bool, len(pool))
	auto := names[:0:0]
	own := make(map[string]Accent)
	for _, name := range names {
		a, ok := ParseAccent(m.sessionAccentString(name))
		if !ok {
			auto = append(auto, name)
			continue
		}
		own[name] = a
		if slot, ok := sessionReservedSlot(a, pool); ok {
			reserved[slot] = true
		}
	}
	colors := assignSessionColors(auto, reserved, pool)
	settleAdjacentRows(names, colors, own, pool, m.terminalBg())
	m.sessionColors = colors
}

// settleAdjacentRows repairs the one case the set-based assignment cannot see:
// past the pool's size a duplicate hue is unavoidable, and nothing in a
// set-based answer stops the two sessions wearing it from standing next to each
// other in the row order a surface is about to draw. Walking the rows in order
// and moving the auto side of each adjacent pair to a hue neither neighbour
// wears fixes that pair without touching any other row, so one left-to-right
// pass is enough, and the hue it moves to is one the pool already had, so
// the repair never invents a colour. An accent the user set never moves; when
// two pinned accents collide the user asked for both, and they stand.
//
// Rows are compared by the colour they show, not by the Accent value: two
// slots a theme paints identically are one hue to a person, and a pass that
// compared values would stand aside from a pair that looks doubled. This is
// the one place the answer depends on the order the rows were listed in. The
// rail's row order is a local drag order, so two clients can repair a
// duplicate pair differently and an over-cap session can wear a different hue
// on each. Up to the pool's size nobody shares and nothing moves, which is
// the case the order-independence guarantee is about.
func settleAdjacentRows(names []string, auto, own map[string]Accent, pool []sessionHue, bg color.Color) {
	shown := func(a Accent) color.Color {
		return overlay.Shown(theme.Readable(a.RGB(), bg))
	}
	sameHue := func(a, b color.Color) bool {
		if a == nil || b == nil {
			return a == nil && b == nil
		}
		return overlay.Distance(a, b) < sessionHueMinGap
	}
	eff := make([]Accent, len(names))
	hues := make([]color.Color, len(names))
	for i, name := range names {
		if a, ok := own[name]; ok {
			eff[i] = a
		} else {
			eff[i] = auto[name]
		}
		hues[i] = shown(eff[i])
	}
	for i := range names {
		if _, pinned := own[names[i]]; pinned {
			continue
		}
		var prevH, nextH color.Color
		if i > 0 {
			prevH = hues[i-1]
		}
		if i+1 < len(eff) {
			nextH = hues[i+1]
		}
		if !sameHue(hues[i], prevH) && !sameHue(hues[i], nextH) {
			continue
		}
		for _, h := range pool {
			if sameHue(h.shown, hues[i]) || sameHue(h.shown, prevH) || sameHue(h.shown, nextH) {
				continue
			}
			eff[i] = SlotAccent(h.slot)
			hues[i] = h.shown
			auto[names[i]] = eff[i]
			break
		}
	}
}

// sessionReservedSlot is the pool position an explicit accent takes out of
// the automatic pool. A named accent says its slot outright. A literal claims
// one only when it is exactly that slot's colour, which is what the picker
// writes when the user lands on the session's own hue: without the match, an
// accent set to the very colour another session was about to be handed would
// not stop it being handed out, and the two would collide in the one way the
// colours exist to prevent. An accent naming a slot the theme's pool dropped
// reserves nothing: no automatic assignment can land on it.
func sessionReservedSlot(a Accent, pool []sessionHue) (int, bool) {
	if a.IsSlot() {
		for i, h := range pool {
			if a.Slot == h.slot {
				return i, true
			}
		}
		return 0, false
	}
	rgb := a.RGB()
	for i, h := range pool {
		if SlotAccent(h.slot).RGB() == rgb {
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
	if !m.Settings.SessionColors || name == "" {
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
	return sessionAutoAccent(name, m.sessionPool()), true
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

// sessionBorderQuiet is how far an unfocused pane's border is pulled back
// toward the ground. Far enough that the focused pane still wins the frame,
// near enough that the hue is still readable as the same one.
const sessionBorderQuiet = 0.55

// sessionBorderTint is the colour every pane's border carries when
// appearance.session_border asks for it, and whether it carries one at all.
//
// The session is the one thing every pane on screen has in common, so its
// colour is what says which session, and therefore which machine, you are
// looking at. That is a question the rail already answers, but the rail is off
// to one side and the borders are around the thing you are reading.
//
// It is measured against the terminal's own background for the same reason
// sessionTint is: a hue from the theme's sixteen is legible on some grounds and
// a smudge on others, and which is which is not a thing to decide by eye.
// It answers both strengths at once so the caller does not have to know how far
// back an unfocused border is pulled.
func (m *OS) sessionBorderTint() (focused, unfocused color.Color, ok bool) {
	if !m.Settings.SessionBorder {
		return nil, nil, false
	}
	bg := m.terminalBg()
	tint := m.sessionTint(m.SessionName, bg)
	if tint == nil {
		return nil, nil, false
	}
	return tint, overlay.MixColors(tint, bg, sessionBorderQuiet), true
}

// rowGround is what a rail row is actually drawn on: the band under the
// pointer or the cursor when it has one, and the terminal's own background
// otherwise, since the rail paints no slab of its own. Contrast is measured
// against this and never against the overlay palette's panel colour, which the
// rail never uses.
func (m *OS) rowGround(rowBg color.Color) color.Color {
	if rowBg != nil {
		return rowBg
	}
	return m.terminalBg()
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
	if !m.Settings.SessionColors {
		return nil
	}
	a, src := m.effectiveAccent(e.WindowID, e.SessionID)
	if src == accentSourceNone {
		return nil
	}
	return theme.Readable(a.RGB(), bg)
}
