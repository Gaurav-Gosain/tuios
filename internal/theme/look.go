package theme

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/Gaurav-Gosain/tuios/internal/atomicfile"
)

// A look (see config.ParseLook) may carry a theme and a glyph set. A stranger
// writes them, so they are checked here as data before anything reaches the
// themes or glyphs directory: the id must be a plain file name, every string
// must be free of control characters (a glyph is printed straight into the
// frame, and lipgloss.Width does not count an escape sequence, so the width
// check alone would let one through), and only the fields the file format
// knows are written back out. The bytes the look held are never written as
// they came.

// MaxLookAssetBytes caps the theme or glyph set a look carries.
const MaxLookAssetBytes = 32 << 10

// CheckLookTheme checks a theme a look carries and returns the theme file to
// write under id.
func CheckLookTheme(id string, data []byte) ([]byte, error) {
	if len(data) > MaxLookAssetBytes {
		return nil, fmt.Errorf("the theme is larger than %d KiB", MaxLookAssetBytes>>10)
	}
	if !ValidThemeID(id) {
		return nil, fmt.Errorf("theme name %q cannot be a file name. Use a-z, 0-9, '-', '_' and '.'", id)
	}
	f, err := readThemeFile(id+".json", data)
	if err != nil {
		return nil, err
	}
	f.ID = id
	if hasControl(f.DisplayName) || hasControl(f.Base) {
		return nil, fmt.Errorf("the theme has a control character in its name")
	}
	// A role is a colour, or a group of colours such as border.unfocused.
	for k, v := range f.Chrome {
		group, isGroup := v.(map[string]any)
		if !isGroup {
			group = map[string]any{"": v}
		}
		for field, c := range group {
			s, ok := c.(string)
			if !ok || hasControl(s) {
				name := k
				if field != "" {
					name += "." + field
				}
				return nil, fmt.Errorf("theme chrome %s must be a colour", name)
			}
		}
	}
	EnsureRegistry()
	r, err := mergeBase(f)
	if err != nil {
		return nil, err
	}
	if _, err := buildChrome(r.chrome, r.tint); err != nil {
		return nil, err
	}
	// Written the way a theme file is: the fields the file set, in the
	// order a person reads a theme.
	d := themeDoc{ID: id, DisplayName: f.DisplayName, Base: f.Base, Dark: f.Dark, Chrome: chromeOf(f.Chrome)}
	for i, c := range themeColours(&f.Tint) {
		if *c != nil {
			d.Colours[i] = tintHex(*c)
		}
	}
	return d.encode(true), nil
}

// CheckLookGlyphs checks a glyph set a look carries and returns the glyph set
// file to write under id, with one line for each glyph it drops.
func CheckLookGlyphs(id string, data []byte) ([]byte, []string, error) {
	if len(data) > MaxLookAssetBytes {
		return nil, nil, fmt.Errorf("the glyph set is larger than %d KiB", MaxLookAssetBytes>>10)
	}
	if !ValidThemeID(id) {
		return nil, nil, fmt.Errorf("glyph set name %q cannot be a file name. Use a-z, 0-9, '-', '_' and '.'", id)
	}
	var set GlyphSet
	if err := json.Unmarshal(data, &set); err != nil {
		return nil, nil, fmt.Errorf("failed to parse glyph set: %w", err)
	}
	set.ID = id
	if set.DisplayName == "" {
		set.DisplayName = id
	}
	fields := []*string{&set.DisplayName, &set.Inherits}
	for _, role := range glyphRoles {
		fields = append(fields, role.field(&set))
	}
	if set.Border != nil {
		fields = append(fields, borderFields(set.Border)...)
	}
	for _, p := range fields {
		if hasControl(*p) {
			return nil, nil, fmt.Errorf("the glyph set has a control character in %q", *p)
		}
	}
	problems := sanitizeGlyphSet(&set)
	out, err := json.MarshalIndent(&set, "", "  ")
	return out, problems, err
}

// WriteLookAsset writes a checked theme or glyph set file, from CheckLookTheme
// or CheckLookGlyphs, into the themes or glyphs directory. The write replaces
// the file in one step and never follows a link at the file's name.
func WriteLookAsset(glyphs bool, id string, data []byte) (string, error) {
	if !ValidThemeID(id) {
		return "", fmt.Errorf("%q cannot be a file name", id)
	}
	dir, err := lookAssetDir(glyphs)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, id+".json")
	if err := atomicfile.Write(path, data, 0o600, false); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}

// RemoveLookAsset removes a theme or glyph set file a look installed. A file
// that is not there is not an error.
func RemoveLookAsset(glyphs bool, id string) error {
	if !ValidThemeID(id) {
		return fmt.Errorf("%q cannot be a file name", id)
	}
	dir, err := lookAssetDir(glyphs)
	if err != nil {
		return err
	}
	err = os.Remove(filepath.Join(dir, id+".json"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// LookAssetThere reports whether a theme or glyph set file is at id. A look
// must not write over a file it did not install.
func LookAssetThere(glyphs bool, id string) bool {
	dir, err := lookAssetDir(glyphs)
	if err != nil || !ValidThemeID(id) {
		return false
	}
	_, err = os.Lstat(filepath.Join(dir, id+".json"))
	return err == nil
}

func lookAssetDir(glyphs bool) (string, error) {
	if glyphs {
		return GetGlyphsDir()
	}
	return GetThemesDir()
}

// hasControl reports whether s holds a control character or a bidi control,
// which reorders the text a terminal shows around it.
func hasControl(s string) bool {
	return strings.IndexFunc(s, IsUnsafeRune) >= 0
}

// IsUnsafeRune reports whether r is a control character or a bidi control.
func IsUnsafeRune(r rune) bool {
	return unicode.IsControl(r) || r == 0x200e || r == 0x200f || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069
}

// RegisterLookTheme checks a theme as CheckLookTheme does and registers it
// under id in this process only. Nothing is written to disk. The browser build
// uses it to show a look's theme in a tab.
func RegisterLookTheme(id string, data []byte) error {
	out, err := CheckLookTheme(id, data)
	if err != nil {
		return err
	}
	f, err := readThemeFile(id+".json", out)
	if err != nil {
		return err
	}
	r, err := mergeBase(f)
	if err != nil {
		return err
	}
	return registerResolved(r)
}
