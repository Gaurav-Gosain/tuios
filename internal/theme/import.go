package theme

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	tint "github.com/lrstanley/bubbletint/v2"
	"tuios.dev/swatch"
	"tuios.dev/swatch/termconfig"
)

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
		return nil, "", errfmt.Errorf("read %s: %w", path, err)
	}
	p, format, err := termconfig.Parse(data)
	if err != nil {
		return nil, "", errfmt.Errorf("%s: %w", path, err)
	}

	if id == "" {
		base := filepath.Base(path)
		id = strings.ToLower(strings.TrimSuffix(base, filepath.Ext(base)))
	}

	t := &tint.Tint{ID: id, DisplayName: id}
	assign(t, p)
	t.Dark = isDark(t.Bg)
	fillDefaults(t)
	return t, format, nil
}

// WriteTheme writes a theme into the custom themes directory and registers it,
// returning the file it wrote.
func WriteTheme(t *tint.Tint) (string, error) {
	if t.ID == "" || t.ID != filepath.Base(t.ID) || strings.HasPrefix(t.ID, ".") || strings.ContainsAny(t.ID, `/\`) {
		return "", errfmt.Errorf("theme name %q cannot be a file name. Use a name with no / or \\ that does not start with a dot", t.ID)
	}
	dir, err := GetThemesDir()
	if err != nil {
		return "", err
	}
	body := fullDoc(t, chromeRaw{}).encode(true)
	path := filepath.Join(dir, t.ID+".json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return "", errfmt.Errorf("write %s: %w", path, err)
	}
	EnsureRegistry()
	tint.Register(t)
	return path, nil
}

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

func isDark(bg *tint.Color) bool {
	if bg == nil {
		return true
	}
	r, g, b, _ := ContrastText(bg).RGBA()
	return r > 0x7fff && g > 0x7fff && b > 0x7fff
}
