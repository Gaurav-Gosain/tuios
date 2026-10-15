package theme

import (
	"errors"
	"image/color"
	"os"
	"sync"
	"sync/atomic"

	tint "github.com/lrstanley/bubbletint/v2"
	"tuios.dev/swatch"
	"tuios.dev/swatch/omarchy"
)

// Omarchy themes.
//
// Omarchy applies one theme to the whole desktop and keeps it in a
// colors.toml (see tuios.dev/swatch/omarchy). tuios reads it in two ways:
//
//   - "omarchy" is the theme Omarchy has applied. It is a live id: its
//     colours change when the person switches the Omarchy theme, and the
//     client follows (internal/app/omarchy_follow.go). An empty
//     appearance.theme means the same on a machine with Omarchy, so tuios
//     follows Omarchy unless told otherwise. "none" keeps the terminal's own
//     colours even there.
//   - "omarchy/NAME" is one installed Omarchy theme, fixed, for the picker and
//     for screenshots.

// Reserved theme ids.
const (
	// OmarchyID follows the theme Omarchy has applied.
	OmarchyID = "omarchy"
	// NoneID keeps the terminal's own colours, even where an empty theme
	// would follow Omarchy.
	NoneID = "none"
	// omarchyPrefix starts the id of one installed Omarchy theme.
	omarchyPrefix = "omarchy/"
)

// ErrOmarchyNotFound is Initialize's error for the omarchy theme on a machine
// without Omarchy.
var ErrOmarchyNotFound = errors.New("omarchy is not installed on this machine")

// OmarchySource is where the applied Omarchy theme is read from. A variable
// so a test can point it at a directory of its own. SWATCH_OMARCHY_DIR does
// the same for a whole process.
var OmarchySource = func() swatch.Source { return omarchy.Current() }

// omarchyState is the palette the omarchy id was last registered with.
var omarchyState struct {
	sync.Mutex
	palette swatch.Palette
	valid   bool
}

// generation counts every change of the active theme, a live one's new
// colours included. See Generation.
var generation atomic.Uint64

// Generation is a number that changes whenever the active theme or its
// colours change. A memo of anything drawn from the theme keys on it rather
// than on the id, because the omarchy id keeps its name while its colours
// change.
func Generation() uint64 { return generation.Load() }

// OmarchyInstalled reports whether Omarchy has applied a theme on this
// machine. It reads one small file.
func OmarchyInstalled() bool {
	_, err := loadOmarchy()
	return err == nil
}

// FollowsOmarchy reports whether the configured theme name means following
// Omarchy here: "omarchy", or no theme on a machine with Omarchy.
func FollowsOmarchy(name string) bool {
	switch name {
	case OmarchyID:
		return true
	case "":
		return OmarchyInstalled()
	}
	return false
}

// Themed reports whether the configured theme name puts a theme in force on
// this machine. An empty name does on a machine with Omarchy.
func Themed(name string) bool {
	switch name {
	case "", OmarchyID:
		return OmarchyInstalled()
	case NoneID:
		return false
	}
	return true
}

// loadOmarchy reads the applied Omarchy theme.
func loadOmarchy() (swatch.Palette, error) {
	p, err := OmarchySource().Load()
	if errors.Is(err, swatch.ErrNotFound) {
		return p, ErrOmarchyNotFound
	}
	return p, err
}

// refreshOmarchy reads the applied Omarchy theme and registers it under the
// omarchy id. It reports whether the id now holds new colours.
func refreshOmarchy() (bool, error) {
	p, err := loadOmarchy()
	if err != nil {
		return false, err
	}
	return SetOmarchyPalette(p), nil
}

// SetOmarchyPalette registers p as the omarchy theme. When omarchy is the
// active theme, the new colours are in force when it returns. It reports
// whether they differ from the ones registered before, and does nothing when
// they do not, so the memos keyed on the theme stay valid.
func SetOmarchyPalette(p swatch.Palette) bool {
	// Before the lock: building the registry registers the omarchy id too,
	// and takes the lock to do it.
	EnsureRegistry()
	omarchyState.Lock()
	defer omarchyState.Unlock()
	if omarchyState.valid && omarchyState.palette.Equal(p) {
		return false
	}
	omarchyState.palette, omarchyState.valid = p, true
	name := "Omarchy"
	if p.Name != "" {
		name += ": " + p.Name
	}
	t, c := FromPalette(OmarchyID, name, p)
	tint.Register(t)
	registerChrome(OmarchyID, c)
	if CurrentThemeID() == OmarchyID {
		generation.Add(1)
	}
	return true
}

// OmarchyPalette returns the palette the omarchy id holds, and false before
// one was read.
func OmarchyPalette() (swatch.Palette, bool) {
	omarchyState.Lock()
	defer omarchyState.Unlock()
	return omarchyState.palette, omarchyState.valid
}

// registerOmarchyThemes registers every installed Omarchy theme as
// omarchy/NAME, and the applied one as omarchy. Runs with the registry, once.
func registerOmarchyThemes() {
	for _, th := range omarchyThemes() {
		p, err := th.Load()
		if err != nil {
			continue
		}
		t, c := FromPalette(omarchyPrefix+th.ID, "Omarchy "+th.Name(), p)
		tint.Register(t)
		registerChrome(t.ID, c)
	}
	if p, err := loadOmarchy(); err == nil {
		omarchyState.Lock()
		omarchyState.palette, omarchyState.valid = p, true
		omarchyState.Unlock()
		name := "Omarchy"
		if p.Name != "" {
			name += ": " + p.Name
		}
		t, c := FromPalette(OmarchyID, name, p)
		tint.Register(t)
		registerChrome(OmarchyID, c)
	}
}

// omarchyThemes lists the installed Omarchy themes. A variable for tests.
var omarchyThemes = func() []omarchy.Theme {
	if os.Getenv(omarchy.EnvDir) != "" {
		// A test that points at a current directory of its own does not
		// want the machine's themes in the list either.
		return nil
	}
	return omarchy.Themes()
}

// initOmarchy makes the applied Omarchy theme the active one. On a machine
// without Omarchy it costs a failed open, and the registry is not built.
func initOmarchy() error {
	if _, err := refreshOmarchy(); err != nil {
		return err
	}
	if !tint.SetTintID(OmarchyID) {
		return ErrOmarchyNotFound
	}
	return nil
}

// FromPalette builds a tuios theme from a swatch palette: the tint the panes
// are painted with, and the chrome. The ANSI colours, the cursor and the
// selection are the palette's own, so a pane looks like a terminal Omarchy
// themed. The chrome takes the accent, the border, the status colours and the
// grounds from their roles.
func FromPalette(id, name string, p swatch.Palette) (*tint.Tint, *Chrome) {
	hex := func(c color.Color) *tint.Color {
		if c == nil {
			return nil
		}
		return tint.FromHex(swatch.Hex(c))
	}
	t := &tint.Tint{
		ID:          id,
		DisplayName: name,
		Dark:        !p.Light,
		Fg:          hex(p.Foreground),
		Bg:          hex(p.Background),
		Cursor:      hex(p.Cursor),
		SelectionBg: hex(p.SelectionBackground),
	}
	slots := [16]**tint.Color{
		&t.Black, &t.Red, &t.Green, &t.Yellow, &t.Blue, &t.Purple, &t.Cyan, &t.White,
		&t.BrightBlack, &t.BrightRed, &t.BrightGreen, &t.BrightYellow,
		&t.BrightBlue, &t.BrightPurple, &t.BrightCyan, &t.BrightWhite,
	}
	for i, slot := range slots {
		*slot = hex(p.ANSI[i])
	}
	fillDefaults(t)

	c := &Chrome{
		Accent:       AsColor(hex(p.Accent)),
		AccentBright: AsColor(hex(p.Border)),
		Success:      AsColor(hex(p.Success)),
		Warning:      AsColor(hex(p.Warning)),
		Error:        AsColor(hex(p.Error)),
		Info:         AsColor(hex(p.Info)),
		Surface:      AsColor(hex(p.Surface)),
		Canvas:       AsColor(hex(p.Raw["darker_background"])),
		Panel:        AsColor(hex(p.Raw["dark_background"])),
	}
	c.deriveRamp()
	return t, c
}

// OmarchyStatus is what tuios found of Omarchy on this machine.
type OmarchyStatus struct {
	Installed bool `json:"installed"`
	// Dir is the current directory the applied theme was read from.
	Dir string `json:"dir,omitempty"`
	// Theme is the name of the applied theme.
	Theme string `json:"theme,omitempty"`
}

// ReadOmarchyStatus reads the applied Omarchy theme and says where it came
// from.
func ReadOmarchyStatus() OmarchyStatus {
	for _, dir := range OmarchySource().WatchPaths() {
		if p, err := omarchy.At(dir).Load(); err == nil {
			return OmarchyStatus{Installed: true, Dir: dir, Theme: p.Name}
		}
	}
	return OmarchyStatus{}
}
