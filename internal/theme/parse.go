package theme

import (
	"fmt"
	"image/color"
	"maps"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"charm.land/lipgloss/v2"
	"charm.land/tone"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/charmbracelet/x/ansi"
	tint "github.com/lrstanley/bubbletint/v2"
)

// One colour vocabulary for every colour tuios reads from a file.
//
// The backgrounds, the border overrides, the scrollbar tint, the selection
// marks, the dock's cell and tab colours, the rail's agent row and a theme's
// chrome object each grew their own: hex here, hex or a keyword there, a few
// palette names somewhere else. A value now means the same thing in all of
// them, and it is one of
//
//   - a hex literal, #rgb or #rrggbb;
//   - an ANSI index, 0 to 255, where 0 to 15 are the sixteen slots below;
//   - a slot name: black, red, green, yellow, blue, magenta (or purple, which
//     is what bubbletint and the theme files call slot 5), cyan, white, each
//     with a bright_ form, and fg and bg for the theme's foreground and
//     background;
//   - a chrome role: accent, accent_bright, success, warning, error, info,
//     surface, canvas, panel, card, and the three ink tiers text, dim and
//     muted, which can be read but never set;
//   - a CharmTone name, such as charple or guac.
//
// Names are looked up in that order, role then slot then CharmTone, so no
// CharmTone colour can hide a slot or a role. A keyword a key already had
// (off, theme, quiet, border, muted) is that key's to check before it asks
// here, and keeps its meaning.
//
// A role or a slot is a reference, not a colour: it is resolved against the
// theme in force, so a value written as "accent" or "bright_magenta" follows a
// theme change, and at 16 colours it is the slot itself, painted from the
// user's own palette. A hex literal is the same colour on every theme.

// colorKind is what a ColorSpec holds.
type colorKind uint8

const (
	kindNone colorKind = iota
	// kindRGB is a fixed colour: hex, CharmTone, or an index past the sixteen.
	kindRGB
	// kindSlot is one of the theme's sixteen, or its fg or bg.
	kindSlot
	// kindRole is a chrome role, read off a palette.
	kindRole
)

// The two slots past the sixteen.
const (
	slotFg = 16
	slotBg = 17
)

// Role is a chrome role a colour value can name.
type Role uint8

// The chrome roles, in the order the theme file's chrome object lists them,
// then the ink tiers.
const (
	RoleNone Role = iota
	RoleAccent
	RoleAccentBright
	RoleSuccess
	RoleWarning
	RoleError
	RoleInfo
	RoleSurface
	RoleCanvas
	RolePanel
	RoleCard
	RoleText
	RoleDim
	RoleMuted
)

// roleNames maps each role to the name a file writes.
var roleNames = [...]string{
	RoleAccent:       "accent",
	RoleAccentBright: "accent_bright",
	RoleSuccess:      "success",
	RoleWarning:      "warning",
	RoleError:        "error",
	RoleInfo:         "info",
	RoleSurface:      "surface",
	RoleCanvas:       "canvas",
	RolePanel:        "panel",
	RoleCard:         "card",
	RoleText:         "text",
	RoleDim:          "dim",
	RoleMuted:        "muted",
}

// String is the name a file writes for r.
func (r Role) String() string {
	if int(r) < len(roleNames) {
		return roleNames[r]
	}
	return ""
}

// Settable reports whether a theme's chrome object may set r. The ink tiers
// are measured from the surface, never named: see Chrome.
func (r Role) Settable() bool { return r >= RoleAccent && r <= RoleCard }

// slotNames maps a slot name to its index. Slot 5 has two names, because the
// theme files say purple and every ANSI table says magenta.
var slotNames = func() map[string]int {
	base := []string{"black", "red", "green", "yellow", "blue", "magenta", "cyan", "white"}
	m := make(map[string]int, 2*len(base)+4)
	for i, n := range base {
		m[n] = i
		m["bright_"+n] = i + 8
	}
	m["purple"], m["bright_purple"] = 5, 13
	m["fg"], m["bg"] = slotFg, slotBg
	return m
}()

// toneNames maps a lower-case CharmTone name to its colour, with the two old
// names CharmTone still keeps as aliases.
var toneNames = func() map[string]tone.Key {
	m := make(map[string]tone.Key, len(tone.Keys())+2)
	for _, k := range tone.Keys() {
		m[strings.ToLower(k.String())] = k
	}
	m["ash"], m["charcoal"] = tone.Ash, tone.Charcoal
	return m
}()

// ColorSpec is a colour value read from a file, parsed. The zero value is
// unset and resolves to nil.
type ColorSpec struct {
	kind colorKind
	rgb  color.Color
	slot int
	role Role
}

// IsZero reports whether c is unset.
func (c ColorSpec) IsZero() bool { return c.kind == kindNone }

// Follows reports whether c follows the theme: a role or a slot, which
// resolve to a different colour on each theme. A fixed colour does not.
func (c ColorSpec) Follows() bool { return c.kind == kindSlot || c.kind == kindRole }

// Role is the chrome role c names, or RoleNone.
func (c ColorSpec) Role() Role {
	if c.kind == kindRole {
		return c.role
	}
	return RoleNone
}

// ColorHelp is the line a validation message ends with: what a colour value
// may be.
const ColorHelp = "Use #rrggbb, a number from 0 to 255, a colour name such as bright_magenta, " +
	"or a theme role such as accent"

// ParseColor reads one colour value. Case, surrounding space, and a dash or a
// space in place of the underscore do not matter. An empty value is the zero
// ColorSpec and no error: every colour key reads empty as unset.
func ParseColor(s string) (ColorSpec, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return ColorSpec{}, nil
	}
	if strings.HasPrefix(s, "#") {
		if c, ok := parseHex(s); ok {
			return ColorSpec{kind: kindRGB, rgb: c}, nil
		}
		return ColorSpec{}, fmt.Errorf("%q is not a colour. A hex colour has 3 or 6 digits after the #", s)
	}
	if n, err := strconv.Atoi(s); err == nil {
		switch {
		case n < 0 || n > 255:
			return ColorSpec{}, fmt.Errorf("%q is not a colour. An ANSI colour number is from 0 to 255", s)
		case n < 16:
			return ColorSpec{kind: kindSlot, slot: n}, nil
		default:
			return ColorSpec{kind: kindRGB, rgb: ansi.IndexedColor(uint8(n))}, nil // #nosec G115 -- checked 16-255 above
		}
	}
	name := nameSpelling.Replace(strings.ToLower(s))
	for r := RoleAccent; r <= RoleMuted; r++ {
		if roleNames[r] == name {
			return ColorSpec{kind: kindRole, role: r}, nil
		}
	}
	if i, ok := slotNames[name]; ok {
		return ColorSpec{kind: kindSlot, slot: i}, nil
	}
	if k, ok := toneNames[name]; ok {
		return ColorSpec{kind: kindRGB, rgb: lipgloss.Color(k.Hex())}, nil
	}
	return ColorSpec{}, fmt.Errorf("%q is not a colour. %s", s, ColorHelp)
}

// nameSpelling makes a dash or a space in a name the underscore.
var nameSpelling = strings.NewReplacer("-", "_", " ", "_")

// parseHex reads #rgb or #rrggbb into the colour lipgloss would make of the
// six-digit form, so a value that was hex before this parser draws the same
// colour after it.
func parseHex(s string) (color.Color, bool) {
	h := s[1:]
	if len(h) != 3 && len(h) != 6 {
		return nil, false
	}
	for _, r := range h {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return nil, false
		}
	}
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	return lipgloss.Color("#" + h), true
}

// On is c's colour on pal: a role is read off pal, so it is the colour the
// surface pal paints already shows for that role, and a slot is the theme's
// at pal's depth. Nil when c is unset.
func (c ColorSpec) On(pal overlay.Palette) color.Color {
	switch c.kind {
	case kindRGB:
		return c.rgb
	case kindSlot:
		return slotColor(c.slot, pal.Depth)
	case kindRole:
		return roleOn(c.role, pal)
	}
	return nil
}

// Resolve is On for a caller that draws on no palette of its own, such as a
// pane border or a pane's selection: a role is read off the dialog palette,
// UI, and only when c is a role.
func (c ColorSpec) Resolve() color.Color {
	switch c.kind {
	case kindRGB:
		return c.rgb
	case kindSlot:
		return slotColor(c.slot, Depth())
	case kindRole:
		return roleOn(c.role, UI())
	}
	return nil
}

// roleOn reads role r off pal. Each role is the palette field a chrome role
// fills (see buildUI): error is Warn, which is the destructive red, and there
// is no role for a palette field a theme cannot set.
func roleOn(r Role, pal overlay.Palette) color.Color {
	switch r {
	case RoleAccent:
		return pal.Accent
	case RoleAccentBright:
		return pal.AccentBright
	case RoleSuccess:
		return pal.Success
	case RoleWarning:
		return pal.Warning
	case RoleError:
		return pal.Warn
	case RoleInfo:
		return pal.Info
	case RoleSurface:
		return pal.Surface
	case RoleCanvas:
		return pal.Canvas
	case RolePanel:
		return pal.Panel
	case RoleCard:
		return pal.Card
	case RoleText:
		return pal.Fg
	case RoleDim:
		return pal.FgDim
	case RoleMuted:
		return pal.FgMute
	}
	return nil
}

// slotTable is the theme's sixteen, fg and bg, as drawn at one depth.
type slotTable struct {
	gen   uint64
	tint  *tint.Tint
	depth overlay.Depth
	cols  [18]color.Color
}

var slotMemo atomic.Pointer[slotTable]

// slotColor is slot i at depth d. At 16 colours, or with no theme, a slot is
// the terminal's own, so the user's palette paints it; fg and bg are then the
// terminal's default colours. With a theme it is the theme's colour, stepped
// down at 256 colours as the chrome's are. The table is built once per theme
// and depth, so a frame only reads it.
func slotColor(i int, d overlay.Depth) color.Color {
	t := Current()
	gen := Generation()
	if tab := slotMemo.Load(); tab != nil && tab.gen == gen && tab.tint == t && tab.depth == d {
		return tab.cols[i]
	}
	tab := &slotTable{gen: gen, tint: t, depth: d}
	var slots [16]*tint.Color
	if t != nil && d != overlay.Depth16 {
		slots = ANSIOrder(t)
	}
	for n := range 16 {
		tab.cols[n] = overlay.Slot(n)
		if slots[n] != nil {
			tab.cols[n] = themeInk(slots[n], d)
		}
	}
	tab.cols[slotFg], tab.cols[slotBg] = overlay.NoColor, overlay.NoColor
	if t != nil && d != overlay.Depth16 {
		if t.Fg != nil {
			tab.cols[slotFg] = themeInk(t.Fg, d)
		}
		if t.Bg != nil {
			tab.cols[slotBg] = themeInk(t.Bg, d)
		}
	}
	slotMemo.Store(tab)
	return tab.cols[i]
}

// themeInk is a theme colour as drawn at depth d.
func themeInk(c *tint.Color, d overlay.Depth) color.Color {
	if d == overlay.Depth256 {
		return overlay.To256(c)
	}
	return c
}

// specMemo keeps every colour value parsed so far. Parsing does not depend on
// the theme, so an entry never goes stale; the memo exists so a renderer that
// holds a value as the string the config wrote reads a parsed one instead of
// parsing on every frame. Copy on write: a frame reads it with no lock.
var specMemo struct {
	mu sync.Mutex
	m  atomic.Pointer[map[string]ColorSpec]
}

// specMemoMax bounds the memo. A config names a handful of colours; the bound
// is for a caller that feeds it what a person types.
const specMemoMax = 256

// SpecOf is ParseColor through the memo, with an invalid value as the zero
// ColorSpec: the validator has already reported it, and a renderer only needs
// to know there is no colour.
func SpecOf(s string) ColorSpec {
	if s == "" {
		return ColorSpec{}
	}
	if m := specMemo.m.Load(); m != nil {
		if c, ok := (*m)[s]; ok {
			return c
		}
	}
	c, _ := ParseColor(s)
	specMemo.mu.Lock()
	defer specMemo.mu.Unlock()
	next := make(map[string]ColorSpec)
	if m := specMemo.m.Load(); m != nil && len(*m) < specMemoMax {
		maps.Copy(next, *m)
	}
	next[s] = c
	specMemo.m.Store(&next)
	return c
}

// ConfigColor is the colour a config value means for a caller with no palette
// of its own, or nil when it is unset or not a colour. See Resolve.
func ConfigColor(s string) color.Color { return SpecOf(s).Resolve() }

// ConfigColorOn is the colour a config value means on pal, or nil when it is
// unset or not a colour. See On.
func ConfigColorOn(s string, pal overlay.Palette) color.Color { return SpecOf(s).On(pal) }

// IsColor reports whether s is a colour value: empty is not. It reads through
// the memo, so a caller on the frame path pays a map lookup, not a parse.
func IsColor(s string) bool { return !SpecOf(s).IsZero() }
