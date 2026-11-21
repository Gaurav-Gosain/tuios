package theme

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/Gaurav-Gosain/tuios/internal/atomicfile"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/units"
)

// MaxLookAssetBytes caps the theme or glyph set a look carries.
const MaxLookAssetBytes = 32 << 10

// CheckLookTheme checks a theme a look carries and returns the theme file to
// write under id.
func CheckLookTheme(id string, data []byte) ([]byte, error) {
	if len(data) > MaxLookAssetBytes {
		return nil, errfmt.Errorf("the theme is larger than %s", units.Bytes(MaxLookAssetBytes))
	}
	if !ValidThemeID(id) {
		return nil, errfmt.Errorf("theme name %q cannot be a file name. Use a-z, 0-9, '-', '_' and '.'", id)
	}
	f, err := readThemeFile(id+".json", data)
	if err != nil {
		return nil, err
	}
	f.ID = id
	if hasControl(f.DisplayName) || hasControl(f.Base) {
		return nil, errfmt.Errorf("the theme has a control character in its name")
	}
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
				return nil, errfmt.Errorf("theme chrome %s must be a colour", name)
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
		return nil, nil, errfmt.Errorf("the glyph set is larger than %s", units.Bytes(MaxLookAssetBytes))
	}
	if !ValidThemeID(id) {
		return nil, nil, errfmt.Errorf("glyph set name %q cannot be a file name. Use a-z, 0-9, '-', '_' and '.'", id)
	}
	var set GlyphSet
	if err := jsonx.Unmarshal(data, &set); err != nil {
		return nil, nil, errfmt.Errorf("failed to parse glyph set: %w", err)
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
			return nil, nil, errfmt.Errorf("the glyph set has a control character in %q", *p)
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
		return "", errfmt.Errorf("%q cannot be a file name", id)
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
		return "", errfmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}

// RemoveLookAsset removes a theme or glyph set file a look installed. A file
// that is not there is not an error.
func RemoveLookAsset(glyphs bool, id string) error {
	if !ValidThemeID(id) {
		return errfmt.Errorf("%q cannot be a file name", id)
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

// ExportGlyphSet returns a glyph set from the glyphs directory as one file,
// with every set it inherits folded in, so it draws the same on a machine
// that has none of those sets. ok is false when id is not a set of the
// person's own, such as a built-in set.
func ExportGlyphSet(id string) (data []byte, ok bool, err error) {
	ReloadGlyphSets()
	glyphMu.RLock()
	_, user := userGlyphSets[id]
	glyphMu.RUnlock()
	if !user {
		return nil, false, nil
	}
	set := ResolveGlyphSet(id)
	set.Inherits = ""
	data, err = jsonx.Marshal(set)
	return data, true, err
}

// IsThemeFile reports whether id is a theme from the themes directory: one
// the person wrote, edited or installed with a look.
func IsThemeFile(id string) bool {
	EnsureRegistry()
	ReloadCustomThemes()
	yours, _ := ThemeFileOf(id)
	return yours
}
