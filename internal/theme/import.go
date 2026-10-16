package theme

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tint "github.com/lrstanley/bubbletint/v2"
	"tuios.dev/swatch"
	"tuios.dev/swatch/termconfig"
)

// Converting a terminal's colour scheme into a tuios theme is the most common
// thing anyone means by "rice this to match my setup": the palette already
// exists on disk, in kitty's conf or ghostty's config or an alacritty or
// wezterm toml, and what is needed is the parsing between there and a theme
// file. The parsers live in tuios.dev/swatch/termconfig, which reads all four
// formats into one palette. What is left here is turning that into a theme.

// ImportFormat names a terminal's colour scheme format.
type ImportFormat = termconfig.Format

// The formats Import understands.
const (
	FormatKitty     = termconfig.Kitty
	FormatGhostty   = termconfig.Ghostty
	FormatAlacritty = termconfig.Alacritty
	FormatWezterm   = termconfig.Wezterm
)

// Import reads a terminal colour scheme and returns it as a tuios theme.
//
// The format is sniffed from the content rather than the extension, because
// none of these formats has one it can be relied on for.
func Import(path, id string) (*tint.Tint, ImportFormat, error) {
	// #nosec G304 - the caller names a file it already has; reading it is the point
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("read %s: %w", path, err)
	}
	p, format, err := termconfig.Parse(data)
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", path, err)
	}

	if id == "" {
		base := filepath.Base(path)
		id = strings.ToLower(strings.TrimSuffix(base, filepath.Ext(base)))
	}

	t := &tint.Tint{ID: id, DisplayName: id}
	assign(t, p)
	// A scheme that names no background is taken as dark, which is what all
	// four formats default to and what every scheme that omits it means.
	t.Dark = isDark(t.Bg)
	fillDefaults(t)
	return t, format, nil
}

// WriteTheme writes a theme into the custom themes directory and registers it,
// returning the file it wrote.
func WriteTheme(t *tint.Tint) (string, error) {
	// The id becomes a file name in the themes directory. A shared theme
	// names its own id, so one with a path in it could write elsewhere.
	if t.ID == "" || t.ID != filepath.Base(t.ID) || strings.HasPrefix(t.ID, ".") || strings.ContainsAny(t.ID, `/\`) {
		return "", fmt.Errorf("theme name %q cannot be a file name. Use a name with no / or \\ that does not start with a dot", t.ID)
	}
	dir, err := GetThemesDir()
	if err != nil {
		return "", err
	}
	// Hex strings and every colour, in the order a person reads a theme.
	body := fullDoc(t, chromeRaw{}).encode(true)
	path := filepath.Join(dir, t.ID+".json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	// Register it here as well as on disk so the process that imported it can
	// select the theme without re-reading the directory.
	EnsureRegistry()
	tint.Register(t)
	return path, nil
}

// assign writes the colours a scheme named onto a theme. Only those: a slot
// the scheme left out keeps fillDefaults' answer, as it always did.
func assign(t *tint.Tint, p swatch.Palette) {
	slots := map[string]**tint.Color{
		termconfig.KeyForeground: &t.Fg,
		termconfig.KeyBackground: &t.Bg,
		termconfig.KeyCursor:     &t.Cursor,
	}
	for i, slot := range [16]**tint.Color{
		&t.Black, &t.Red, &t.Green, &t.Yellow, &t.Blue, &t.Purple, &t.Cyan, &t.White,
		&t.BrightBlack, &t.BrightRed, &t.BrightGreen, &t.BrightYellow,
		&t.BrightBlue, &t.BrightPurple, &t.BrightCyan, &t.BrightWhite,
	} {
		slots[termconfig.ANSIKey(i)] = slot
	}
	for key, c := range p.Raw {
		if slot, ok := slots[key]; ok {
			*slot = tint.FromHex(swatch.Hex(c))
		}
	}
}

// isDark reports whether a background wants light text on it. ContrastText
// already answers exactly that question for the chrome, so asking it here means
// an imported theme's dark flag and what the renderer does with the background
// cannot disagree.
func isDark(bg *tint.Color) bool {
	if bg == nil {
		return true
	}
	r, g, b, _ := ContrastText(bg).RGBA()
	return r > 0x7fff && g > 0x7fff && b > 0x7fff
}
