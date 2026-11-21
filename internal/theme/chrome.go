package theme

import (
	"image/color"
	"strings"
	"sync"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	tint "github.com/lrstanley/bubbletint/v2"
)

// A theme's own colours for tuios's furniture, separate from the sixteen the
// panes are painted with.
//
// The sixteen ANSI slots did double duty: they are the emulator's colour table
// and they were also where every chrome colour came from. The accent is
// bright_blue, the focused border is bright_cyan, the mode pills are
// bright_blue, bright_green and yellow, and the notification severities are the
// normal red, yellow, green and blue. So a palette whose accent is amber could
// only have an amber logo by putting amber in bright_blue, which also recolours
// every bold blue a program prints inside a pane. The choice was a faithful
// palette with off-accent chrome, or matching chrome and lying to ls.
//
// A theme may now name these colours directly. Every field is optional and an
// absent one derives exactly as it did before, so a theme file written before
// this existed renders identically.
type Chrome struct {
	// Accent is the primary chrome colour: the logo, the selected row, the
	// window-mode pill. Derived from bright_blue.
	Accent color.Color
	// AccentBright is the secondary accent and the focused window border in
	// window-management mode. Derived from bright_cyan.
	AccentBright color.Color
	// Success is the terminal-mode pill, the focused border in terminal mode,
	// and a success notification. Derived from bright_green.
	Success color.Color
	// Warning is the copy-mode pill and a warning notification. Derived from
	// yellow.
	Warning color.Color
	// Error is an error notification and the chrome's alert ink. Derived from
	// red, and from bright_red for the alert ink.
	Error color.Color
	// Info is an info notification. Derived from blue.
	Info color.Color

	// Surface is the ground every dialog is filled with: the command palette,
	// the pickers, the context menu, the which-key window. It is the one
	// neutral a theme names, and the other three steps of the ramp and the
	// three ink tiers are derived from it at the spacings the constant ramp
	// has, so a panel still reads as raised and a card as inset, and the
	// hierarchy of the text on them is kept. Nil keeps the constant ramp.
	Surface color.Color
	// Canvas, Panel and Card are the other steps of the neutral ramp, for a
	// theme that wants an exact ramp rather than a generated one. Each one
	// left unnamed derives from Surface, and all three are constants when
	// Surface is too.
	Canvas color.Color
	Panel  color.Color
	Card   color.Color

	fg, fgDim, fgMute color.Color

	derived [RoleCard + 1]bool

	furniture [roleLastSettable + 1]color.Color
}

// Furniture is the colour the theme names for one of the roles after
// RoleCard, or nil.
func (c *Chrome) Furniture(r Role) color.Color {
	if c == nil || r <= RoleCard || r > roleLastSettable {
		return nil
	}
	return c.furniture[r]
}

// ChromeNames reports whether the theme in force names role r in its chrome.
func ChromeNames(r Role) bool { return chromeFurniture(r) != nil }

func chromeFurniture(r Role) color.Color {
	c := CurrentChrome()
	if c == nil || Depth() == overlay.Depth16 {
		return nil
	}
	return c.Furniture(r)
}

var chromeRegistry struct {
	sync.RWMutex
	byID map[string]*Chrome
}

func registerChrome(id string, c *Chrome) {
	if id == "" {
		return
	}
	chromeRegistry.Lock()
	defer chromeRegistry.Unlock()
	if chromeRegistry.byID == nil {
		chromeRegistry.byID = make(map[string]*Chrome)
	}
	if c == nil {
		delete(chromeRegistry.byID, id)
		return
	}
	chromeRegistry.byID[id] = c
}

// CurrentChrome is the chrome the active theme names, or nil when it names
// none, which is every built-in theme and every file written without a chrome
// object.
func CurrentChrome() *Chrome {
	t := Current()
	if t == nil {
		return nil
	}
	chromeRegistry.RLock()
	defer chromeRegistry.RUnlock()
	return chromeRegistry.byID[t.ID]
}

type chromeRaw [roleLastSettable + 1]string

func buildChrome(raw chromeRaw, t *tint.Tint) (*Chrome, error) {
	var specs [roleLastSettable + 1]ColorSpec
	for r := RoleAccent; r <= roleLastSettable; r++ {
		specs[r] = parseChromeSpec(raw[r])
	}
	var got [roleLastSettable + 1]color.Color
	for r := RoleAccent; r <= roleLastSettable; r++ {
		c, err := chromeRole(r, &specs, t, nil)
		if err != nil {
			return nil, err
		}
		got[r] = c
	}
	c := &Chrome{
		Accent: got[RoleAccent], AccentBright: got[RoleAccentBright], Success: got[RoleSuccess],
		Warning: got[RoleWarning], Error: got[RoleError], Info: got[RoleInfo],
		Surface: got[RoleSurface], Canvas: got[RoleCanvas], Panel: got[RolePanel], Card: got[RoleCard],
	}
	copy(c.furniture[RoleCard+1:], got[RoleCard+1:])
	named := false
	for _, g := range got {
		named = named || g != nil
	}
	if !named {
		return nil, nil
	}
	c.deriveRamp()
	return c, nil
}

func parseChromeSpec(s string) ColorSpec {
	s = strings.TrimSpace(s)
	if c, ok := overlay.ParseHex(s); ok {
		return ColorSpec{kind: kindRGB, rgb: c}
	}
	c, err := ParseColor(s)
	if err != nil || (c.kind == kindRole && !c.role.Settable()) {
		return ColorSpec{}
	}
	return c
}

var chromeFallbackSlot = [roleLastSettable + 1]int8{
	RoleAccent: 12, RoleAccentBright: 14, RoleSuccess: 10, RoleWarning: 3,
	RoleError: 1, RoleInfo: 4,
	RoleSurface: -1, RoleCanvas: -1, RolePanel: -1, RoleCard: -1,
	RoleBorderUnfocused: 1, RoleBorderCopy: 3, RoleBorderMultifocus: 3, RoleRule: -1,
	RoleAttention: 3, RoleBusy: 4, RoleDotClose: 9, RoleDotMinimize: 11, RoleDotZoom: 10,
	RoleDockGround: -1, RoleRailGround: -1,
}

func chromeRole(r Role, specs *[roleLastSettable + 1]ColorSpec, t *tint.Tint, path []Role) (color.Color, error) {
	for i, p := range path {
		if p == r {
			return nil, chromeLoopError(path[i:])
		}
	}
	c := specs[r]
	switch c.kind {
	case kindRGB:
		return c.rgb, nil
	case kindSlot:
		return chromeSlot(c.slot, t), nil
	case kindRole:
		return chromeRole(c.role, specs, t, append(path, r))
	}
	if len(path) == 0 || chromeFallbackSlot[r] < 0 {
		return nil, nil
	}
	return chromeSlot(int(chromeFallbackSlot[r]), t), nil
}

func chromeSlot(i int, t *tint.Tint) color.Color {
	if t == nil {
		return nil
	}
	switch i {
	case slotFg:
		return AsColor(t.Fg)
	case slotBg:
		return AsColor(t.Bg)
	}
	return AsColor(ANSIOrder(t)[i])
}

func chromeLoopError(loop []Role) error {
	if len(loop) == 1 {
		return errfmt.Errorf("chrome.%s names itself. Set it to a colour", loop[0])
	}
	return errfmt.Errorf("chrome.%s and chrome.%s name each other in a loop. Set one of them to a colour",
		loop[0], loop[len(loop)-1])
}

func (c *Chrome) deriveRamp() {
	if c.Surface == nil {
		return
	}
	if c.Canvas == nil {
		c.derived[RoleCanvas] = true
		c.Canvas = overlay.Darker(c.Surface, chromeRamp.canvas)
	}
	if c.Panel == nil {
		c.derived[RolePanel] = true
		c.Panel = overlay.Darker(c.Surface, chromeRamp.panel)
	}
	if c.Card == nil {
		c.derived[RoleCard] = true
		c.Card = overlay.Lighter(c.Surface, chromeRamp.card)
	}
	text := []color.Color{c.Canvas, c.Panel, c.Surface, c.Card}
	c.fg = overlay.Tone(c.Surface, chromeRamp.fg)
	c.fgDim = overlay.Tone(c.Surface, chromeRamp.fgDim)
	c.fgMute = overlay.Tone(c.Surface, chromeRamp.fgMute)
	for _, g := range text {
		c.fg = overlay.ReadableAt(c.fg, g, ContrastFloor)
		c.fgDim = overlay.ReadableAt(c.fgDim, g, ContrastFloor)
	}
	for _, g := range text[:3] {
		c.fgMute = overlay.ReadableAt(c.fgMute, g, MarkFloor)
	}
}

func chromeOr(pick func(*Chrome) color.Color, fallback color.Color) color.Color {
	if c := CurrentChrome(); c != nil {
		if got := pick(c); got != nil {
			return got
		}
	}
	return fallback
}
