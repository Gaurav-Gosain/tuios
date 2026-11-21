package app

import (
	"image/color"
	"slices"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

const sessionAccentSlotCount = 10

var sessionAccentSlots = [sessionAccentSlotCount]int{
	1, 8,
	2, 9,
	4, 11,
	5, 12,
	3,
	6,
}

type sessionHue struct {
	slot  int
	shown color.Color
}

const sessionHueMinGap = 0.05

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

type sessionPoolKey struct {
	slots [sessionAccentSlotCount]color.RGBA
	bg    color.RGBA
	depth overlay.Depth
}

func (m *OS) sessionPool() []sessionHue {
	var key sessionPoolKey
	for i, idx := range sessionAccentSlots {
		key.slots[i] = SlotAccent(idx).RGB()
	}
	key.bg = toRGBA(m.terminalBg())
	key.depth = overlay.CurrentDepth()
	if m.sessionPoolCache != nil && m.sessionPoolKey == key {
		return m.sessionPoolCache
	}
	m.sessionPoolCache = sessionAccentPool(m.terminalBg())
	m.sessionPoolKey = key
	return m.sessionPoolCache
}

var sessionAccentNames = map[string]int{
	"brightblack": 0, "brightred": 1, "brightgreen": 2, "brightyellow": 3,
	"brightblue": 4, "brightpurple": 5, "brightmagenta": 5, "brightcyan": 6,
	"brightwhite": 7,
	"black":       0,
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

func sessionPreferredSlot(name string, size int) int {
	const prime = 1099511628211
	h := uint64(1469598103934665603)
	for i := range len(name) {
		h ^= uint64(name[i])
		h *= prime
	}
	return int(h % uint64(size))
}

func sessionAutoAccent(name string, pool []sessionHue) Accent {
	return SlotAccent(pool[sessionPreferredSlot(name, len(pool))].slot)
}

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
		taken[slot] = true
		out[name] = SlotAccent(pool[slot].slot)
	}
	return out
}

func (m *OS) refreshSessionColorsFor(sessions []sessiontree.Node) {
	names := make([]string, 0, len(sessions))
	for i := range sessions {
		names = append(names, sessions[i].ID)
	}
	m.refreshSessionColors(names)
}

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

func (m *OS) sessionTint(name string, bg color.Color) color.Color {
	a, ok := m.SessionColor(name)
	if !ok {
		return nil
	}
	return theme.Readable(a.RGB(), bg)
}

const sessionBorderQuiet = 0.55

func (m *OS) sessionBorderTint() (window, terminal, unfocused color.Color, ok bool) {
	if !m.Settings.SessionBorder {
		return nil, nil, nil, false
	}
	bg := m.terminalBg()
	tint := m.sessionTint(m.SessionName, bg)
	if tint == nil {
		return nil, nil, nil, false
	}
	window, terminal = theme.SessionBorderModes(tint, m.host.bg)
	return window, terminal, overlay.MixColors(window, bg, sessionBorderQuiet), true
}

func (m *OS) rowGround(rowBg color.Color) color.Color {
	if rowBg != nil {
		return rowBg
	}
	return m.terminalBg()
}

type accentSource uint8

const (
	accentSourceNone accentSource = iota
	accentSourceOwn
	accentSourceSession
	accentSourceAuto
	accentSourceDefault
)

func (s accentSource) derived() bool {
	return s == accentSourceSession || s == accentSourceAuto || s == accentSourceDefault
}

func (m *OS) effectiveAccent(windowID, sessionID string) (Accent, accentSource) {
	if a, ok := m.WindowAccent(windowID); ok {
		return a, accentSourceOwn
	}
	if a, ok := m.SessionColor(sessionID); ok {
		return a, accentSourceSession
	}
	return Accent{}, accentSourceNone
}

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
