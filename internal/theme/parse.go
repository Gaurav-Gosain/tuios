package theme

import (
	"fmt"
	"image/color"
	"strconv"
	"strings"
	"sync"

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
	// The roles below RoleCard name one piece of furniture each. A theme file
	// writes them in nested objects (see chromeGroups), so their names carry
	// the object: "border.unfocused" is chrome.border.unfocused. Each one left
	// unset draws what it drew before the role existed.
	RoleBorderUnfocused
	RoleBorderCopy
	RoleBorderMultifocus
	RoleRule
	RoleAttention
	RoleBusy
	RoleDotClose
	RoleDotMinimize
	RoleDotZoom
	RoleDockGround
	RoleRailGround
	RoleText
	RoleDim
	RoleMuted
)

// roleLastSettable is the last role a theme's chrome object may set. The ink
// tiers after it are measured, never named.
const roleLastSettable = RoleRailGround

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

	RoleBorderUnfocused:  "border.unfocused",
	RoleBorderCopy:       "border.copy",
	RoleBorderMultifocus: "border.multifocus",
	RoleRule:             "rule",
	RoleAttention:        "status.attention",
	RoleBusy:             "status.busy",
	RoleDotClose:         "dots.close",
	RoleDotMinimize:      "dots.minimize",
	RoleDotZoom:          "dots.zoom",
	RoleDockGround:       "dock.ground",
	RoleRailGround:       "rail.ground",

	RoleText:  "text",
	RoleDim:   "dim",
	RoleMuted: "muted",
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
func (r Role) Settable() bool { return r >= RoleAccent && r <= roleLastSettable }

// slotNames are the eight base slot names, by index. Each has a bright_ form
// eight higher, and slot 5 has two names, because the theme files say purple
// and every ANSI table says magenta.
var slotNames = [8]string{"black", "red", "green", "yellow", "blue", "magenta", "cyan", "white"}

// slotIndex is the slot a name means, or false.
func slotIndex(name string) (int, bool) {
	switch name {
	case "fg":
		return slotFg, true
	case "bg":
		return slotBg, true
	}
	bright := 0
	if rest, ok := strings.CutPrefix(name, "bright_"); ok {
		name, bright = rest, 8
	}
	if name == "purple" {
		name = "magenta"
	}
	for i, n := range slotNames {
		if n == name {
			return i + bright, true
		}
	}
	return 0, false
}

// toneColor is the CharmTone colour with lower-case name, or false. It also
// takes the two old names CharmTone keeps as aliases. A plain search: a value
// is parsed once (see SpecOf), and a map would be built at every start.
func toneColor(name string) (color.Color, bool) {
	switch name {
	case "ash":
		name = "sash"
	case "charcoal":
		name = "char"
	}
	for _, k := range tone.Keys() {
		if strings.EqualFold(k.String(), name) {
			// Copied out of the Key, whose RGBA is a map lookup, so a frame
			// that draws it reads a plain value.
			r, g, b, _ := k.RGBA()
			return color.RGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: 0xff}, true
		}
	}
	return nil, false
}

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
	switch {
	case s == "":
		return ColorSpec{}, nil
	case s[0] == '#':
		// overlay.ParseHex makes the color.RGBA lipgloss.Color makes, so a
		// value that was hex before this parser draws the same colour.
		if c, ok := overlay.ParseHex(s); ok {
			return ColorSpec{kind: kindRGB, rgb: c}, nil
		}
	case s[0] >= '0' && s[0] <= '9':
		n, err := strconv.Atoi(s)
		switch {
		case err != nil || n > 255:
		case n < 16:
			return ColorSpec{kind: kindSlot, slot: n}, nil
		default:
			return ColorSpec{kind: kindRGB, rgb: ansi.IndexedColor(uint8(n))}, nil // #nosec G115 -- checked 16-255 above
		}
	default:
		name := colorName(s)
		for r := RoleAccent; r <= RoleMuted; r++ {
			if roleNames[r] == name {
				return ColorSpec{kind: kindRole, role: r}, nil
			}
		}
		if i, ok := slotIndex(name); ok {
			return ColorSpec{kind: kindSlot, slot: i}, nil
		}
		if c, ok := toneColor(name); ok {
			return ColorSpec{kind: kindRGB, rgb: c}, nil
		}
	}
	return ColorSpec{}, fmt.Errorf("%q is not a colour. %s", s, ColorHelp)
}

// colorName is s in lower case with a dash or a space as the underscore.
func colorName(s string) string {
	b := []byte(s)
	for i, c := range b {
		switch {
		case c >= 'A' && c <= 'Z':
			b[i] = c + 'a' - 'A'
		case c == '-', c == ' ':
			b[i] = '_'
		}
	}
	return string(b)
}

// On is c's colour on pal: a role is read off pal, so it is the colour the
// surface pal paints already shows for that role, and a slot is the theme's
// at pal's depth. A nil pal is for a caller that draws on no palette of its
// own, such as a pane border or a pane's selection: a role is then read off
// the dialog palette, UI, and only when c is a role. Nil when c is unset.
func (c ColorSpec) On(pal *overlay.Palette) color.Color {
	switch c.kind {
	case kindRGB:
		return c.rgb
	case kindSlot:
		if pal == nil {
			return slotColor(c.slot, Depth())
		}
		return slotColor(c.slot, pal.Depth)
	case kindRole:
		if pal == nil {
			ui := UI()
			pal = &ui
		}
		return roleOn(c.role, pal)
	}
	return nil
}

// Resolve is On with no palette of its own. See On.
func (c ColorSpec) Resolve() color.Color { return c.On(nil) }

// roleOn reads role r off pal. Each role is the palette field a chrome role
// fills (see buildUI): error is Warn, which is the destructive red, and there
// is no role for a palette field a theme cannot set.
func roleOn(r Role, pal *overlay.Palette) color.Color {
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
	case RoleAttention:
		return pal.Attention
	case RoleBusy:
		return pal.Busy
	}
	return furnitureRole(r)
}

// slotColor is slot i at depth d. At 16 colours, or with no theme, a slot is
// the terminal's own, so the user's palette paints it, and fg and bg are the
// terminal's default colours. With a theme it is the theme's colour. Reading it
// is a field read off the theme in force, as a role is off a palette, so a
// theme change shows on the next frame with nothing to invalidate.
func slotColor(i int, d overlay.Depth) color.Color {
	var c *tint.Color
	if t := Current(); t != nil && d != overlay.Depth16 {
		switch i {
		case slotFg:
			c = t.Fg
		case slotBg:
			c = t.Bg
		default:
			c = ANSIOrder(t)[i]
		}
	}
	switch {
	case c != nil:
		return c
	case i < 16:
		return overlay.Slot(i)
	}
	return overlay.NoColor
}

// specMemo keeps every colour value parsed so far. Parsing does not depend on
// the theme, so an entry never goes stale; the memo exists so a renderer that
// holds a value as the string the config wrote reads a parsed one instead of
// parsing on every frame.
var specMemo struct {
	sync.RWMutex
	m map[string]ColorSpec
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
	specMemo.RLock()
	c, ok := specMemo.m[s]
	specMemo.RUnlock()
	if ok {
		return c
	}
	c, _ = ParseColor(s)
	specMemo.Lock()
	if specMemo.m == nil || len(specMemo.m) >= specMemoMax {
		specMemo.m = make(map[string]ColorSpec)
	}
	specMemo.m[s] = c
	specMemo.Unlock()
	return c
}

// ConfigColor is the colour a config value means for a caller with no palette
// of its own, or nil when it is unset or not a colour. See Resolve.
func ConfigColor(s string) color.Color { return SpecOf(s).Resolve() }

// ConfigColorOn is the colour a config value means on pal, or nil when it is
// unset or not a colour. See On.
func ConfigColorOn(s string, pal *overlay.Palette) color.Color { return SpecOf(s).On(pal) }

// IsColor reports whether s is a colour value: empty is not. It reads through
// the memo, so a caller on the frame path pays a map lookup, not a parse.
func IsColor(s string) bool { return !SpecOf(s).IsZero() }
