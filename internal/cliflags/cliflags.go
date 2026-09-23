// Package cliflags holds the interface flags every binary that renders the
// TUI registers: `tuios` and its TUI commands, `tuios ssh`, and `tuios-web`.
//
// They live here so the binaries cannot drift apart and each one accepts every
// override on the command line. The package is kept apart from internal/config so the
// widely imported config package does not pick up a pflag dependency.
package cliflags

import (
	"github.com/spf13/pflag"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// Interface is the values of the interface flags. The zero value applies
// nothing, which is what a flag left unset means.
type Interface struct {
	ASCIIOnly            bool
	ThemeName            string
	BorderStyle          string
	DockbarPosition      string
	HideWindowButtons    bool
	WindowButtonStyle    string
	WindowButtonPosition string
	HideScrollbar        bool
	ScrollbackLines      int
	// ShowKeys turns the key display overlay on. It is a session option
	// rather than an appearance override, so Overrides leaves it out.
	ShowKeys            bool
	NoAnimations        bool
	ConfirmQuit         bool
	WindowTitlePosition string
	HideClock           bool
	ShowClock           bool
	ShowCPU             bool
	ShowRAM             bool
	SharedBorders       bool
	ZoomMaxWidth        int
}

// Register adds the interface flags to fs, bound to i. Calling it for several
// flag sets with the same i binds them all to one set of values, which is how
// `tuios` gives each of its TUI commands the same flags.
func (i *Interface) Register(fs *pflag.FlagSet) {
	fs.BoolVar(&i.ASCIIOnly, "ascii-only", false, "Use ASCII characters instead of Nerd Font icons")
	fs.StringVar(&i.ThemeName, "theme", "", "Color theme to use (e.g., dracula, nord, tokyonight). Leave empty to use standard terminal colors without theming")
	fs.StringVar(&i.BorderStyle, "border-style", "", "Window border style: rounded, normal, thick, double, hidden, block, ascii, outer-half-block, inner-half-block (default: from config or rounded)")
	fs.StringVar(&i.DockbarPosition, "dockbar-position", "", "Dockbar position: bottom, top, hidden (default: from config or top)")
	fs.BoolVar(&i.HideWindowButtons, "hide-window-buttons", false, "Hide window control buttons (minimize, maximize, close)")
	fs.StringVar(&i.WindowButtonStyle, "window-button-style", "", "Window control style: pill, dots (default: from config or dots)")
	fs.StringVar(&i.WindowButtonPosition, "window-button-position", "", "Which end of the title bar the window controls sit on: right, left (default: from config or left)")
	fs.BoolVar(&i.HideScrollbar, "hide-scrollbar", false, "Hide the window scrollbar thumb on the border")
	fs.IntVar(&i.ScrollbackLines, "scrollback-lines", 0, "Number of lines to keep in scrollback buffer (default: from config or 10000, min: 100, max: 1000000)")
	fs.BoolVar(&i.ShowKeys, "show-keys", false, "Enable showkeys overlay to display pressed keys")
	fs.BoolVar(&i.NoAnimations, "no-animations", false, "Disable UI animations for instant transitions")
	fs.BoolVar(&i.ConfirmQuit, "confirm-quit", false, "Always show quit confirmation dialog")
	fs.StringVar(&i.WindowTitlePosition, "window-title-position", "", "Window title position: bottom, top, hidden (default: from config or top)")
	fs.BoolVar(&i.HideClock, "hide-clock", false, "Hide the clock overlay (deprecated, clock is hidden by default)")
	fs.BoolVar(&i.ShowClock, "show-clock", false, "Show the clock overlay")
	fs.BoolVar(&i.ShowCPU, "show-cpu", false, "Show CPU graph in the dock")
	fs.BoolVar(&i.ShowRAM, "show-ram", false, "Show RAM usage in the dock")
	fs.BoolVar(&i.SharedBorders, "shared-borders", false, "Share borders between adjacent tiled windows")
	fs.IntVar(&i.ZoomMaxWidth, "zoom-max-width", 0, "Max width in cells for zoom mode (0 = fullscreen, e.g. 120)")
}

// Overrides is the flags as the config overrides they layer over the file.
// ShowKeys is not one; callers pass it to the session on its own.
func (i *Interface) Overrides() config.Overrides {
	return config.Overrides{
		ASCIIOnly:            i.ASCIIOnly,
		BorderStyle:          i.BorderStyle,
		DockbarPosition:      i.DockbarPosition,
		HideWindowButtons:    i.HideWindowButtons,
		WindowButtonStyle:    i.WindowButtonStyle,
		WindowButtonPosition: i.WindowButtonPosition,
		HideScrollbar:        i.HideScrollbar,
		WindowTitlePosition:  i.WindowTitlePosition,
		HideClock:            i.HideClock,
		ShowClock:            i.ShowClock,
		ShowCPU:              i.ShowCPU,
		ShowRAM:              i.ShowRAM,
		SharedBorders:        i.SharedBorders,
		ZoomMaxWidth:         i.ZoomMaxWidth,
		ScrollbackLines:      i.ScrollbackLines,
		NoAnimations:         i.NoAnimations,
		ConfirmQuit:          i.ConfirmQuit,
		ThemeName:            i.ThemeName,
	}
}
