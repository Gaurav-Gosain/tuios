package theme

import (
	"encoding/json"
	"image/color"
	"strings"
	"sync"

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
	}
	if c.Accent == nil && c.AccentBright == nil && c.Success == nil &&
		c.Warning == nil && c.Error == nil && c.Info == nil {
		// An empty or entirely unparseable object is the same as none, and
		// saying so keeps CurrentChrome's nil meaning "derive everything".
		return nil
	}
	return c
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
