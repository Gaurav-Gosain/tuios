package theme

import (
	"encoding/json"
	"image/color"
	"strings"
	"sync"

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

	// The ink tiers, measured against Surface when one is named. Never named
	// in the file: a text colour a theme could set is a text colour that can
	// be set unreadable, and the tiers are their ratios rather than their hex.
	fg, fgDim, fgMute color.Color
}

// chromeFile is the shape read from a theme JSON's optional "chrome" object.
// Strings rather than colours so an unparseable entry can be dropped on its own
// rather than failing the theme.
type chromeFile struct {
	Chrome *struct {
		Accent       string `json:"accent"`
		AccentBright string `json:"accent_bright"`
		Success      string `json:"success"`
		Warning      string `json:"warning"`
		Error        string `json:"error"`
		Info         string `json:"info"`
		Surface      string `json:"surface"`
		Canvas       string `json:"canvas"`
		Panel        string `json:"panel"`
		Card         string `json:"card"`
	} `json:"chrome"`
}

var chromeRegistry struct {
	sync.RWMutex
	byID map[string]*Chrome
}

// registerChrome files a theme's chrome under its id, replacing any earlier
// entry so a re-read of an edited file wins.
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

// parseChrome reads the chrome object out of a theme file's bytes. A field that
// is absent or unparseable is left nil and derives from the ANSI slots as
// before: a typo in one colour costs that colour, not the theme.
func parseChrome(data []byte) *Chrome {
	var f chromeFile
	if err := json.Unmarshal(data, &f); err != nil || f.Chrome == nil {
		return nil
	}
	c := &Chrome{
		Accent:       parseChromeColor(f.Chrome.Accent),
		AccentBright: parseChromeColor(f.Chrome.AccentBright),
		Success:      parseChromeColor(f.Chrome.Success),
		Warning:      parseChromeColor(f.Chrome.Warning),
		Error:        parseChromeColor(f.Chrome.Error),
		Info:         parseChromeColor(f.Chrome.Info),
		Surface:      parseChromeColor(f.Chrome.Surface),
		Canvas:       parseChromeColor(f.Chrome.Canvas),
		Panel:        parseChromeColor(f.Chrome.Panel),
		Card:         parseChromeColor(f.Chrome.Card),
	}
	named := false
	for _, got := range []color.Color{c.Accent, c.AccentBright, c.Success, c.Warning,
		c.Error, c.Info, c.Surface, c.Canvas, c.Panel, c.Card} {
		named = named || got != nil
	}
	if !named {
		// An empty or entirely unparseable object is the same as none, and
		// saying so keeps CurrentChrome's nil meaning "derive everything".
		return nil
	}
	c.deriveRamp()
	return c
}

// deriveRamp fills the neutral steps and ink tiers a theme left to its named
// Surface. It runs once, when the file is read, because UI() is asked for on
// every overlay of every frame and each derivation bisects over contrast
// ratios; the palette a frame copies out has the answers already.
func (c *Chrome) deriveRamp() {
	if c.Surface == nil {
		return
	}
	if c.Canvas == nil {
		c.Canvas = overlay.Darker(c.Surface, chromeRamp.canvas)
	}
	if c.Panel == nil {
		c.Panel = overlay.Darker(c.Surface, chromeRamp.panel)
	}
	if c.Card == nil {
		c.Card = overlay.Lighter(c.Surface, chromeRamp.card)
	}
	// Each tier is measured on Surface at its target, then held to its floor
	// on the other steps it is written on. On a dark ramp the floors never
	// bind: the grounds below Surface only add contrast to a light ink, and
	// the card is where quiet ink already fails and is lifted at the one call
	// site that writes on it. On a light ramp the sides swap. The dark inks
	// measure worst on the canvas, and a quiet tier held to 3.07:1 on the
	// surface measures about 2.1:1 there, which is below the floor the constant
	// palette clears. Lifting only where a floor binds keeps the hierarchy on
	// every ground a theme can ask for, at the cost of compressing its quiet end
	// on a light one.
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

// parseChromeColor accepts the hex spellings a theme file already uses for its
// sixteen, and answers nil for anything else.
func parseChromeColor(s string) color.Color {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if !strings.HasPrefix(s, "#") {
		s = "#" + s
	}
	if len(s) != 4 && len(s) != 7 {
		return nil
	}
	for _, r := range s[1:] {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return nil
		}
	}
	return tint.FromHex(s)
}

// chromeOr returns the theme's colour for a role, or fallback when the theme
// names none.
func chromeOr(pick func(*Chrome) color.Color, fallback color.Color) color.Color {
	if c := CurrentChrome(); c != nil {
		if got := pick(c); got != nil {
			return got
		}
	}
	return fallback
}
