package theme

import (
	"encoding/json"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	tint "github.com/lrstanley/bubbletint/v2"
)

// A theme file may name a base: another theme it starts from. What the file
// leaves out comes from the base, not from the xterm defaults, so
//
//	{ "base": "dracula", "chrome": { "accent": "bright_red" } }
//
// is Dracula with a red accent. The base is a built-in theme or another file
// in the themes directory, and a file may name a base that names a base. The
// colours are merged first and the chrome references resolved after, so a
// role or slot name in the file reads the merged theme.

// themeFile is a theme file as written. Base, Dark and Chrome sit beside the
// embedded tint so one decode reads the whole file; Dark shadows the tint's
// own so an unset one can be told from false, and inherit the base's.
type themeFile struct {
	path string
	tint.Tint
	Base string `json:"base,omitempty"`
	Dark *bool  `json:"dark,omitempty"`
	// Chrome holds any JSON value so a field that is not a string costs that
	// field, as an unparseable colour does, and not the whole theme.
	Chrome map[string]any `json:"chrome,omitempty"`
}

// readThemeFile parses a theme file's bytes. The id comes from the file name
// when the file names none.
func readThemeFile(path string, data []byte) (*themeFile, error) {
	f := &themeFile{path: path}
	if err := json.Unmarshal(data, f); err != nil {
		return nil, fmt.Errorf("failed to parse theme JSON: %w", err)
	}
	f.Base = strings.TrimSpace(f.Base)
	if f.ID == "" {
		base := filepath.Base(path)
		f.ID = strings.ToLower(strings.TrimSuffix(base, filepath.Ext(base)))
	}
	if f.ID == "" {
		return nil, fmt.Errorf("theme has no ID")
	}
	if f.DisplayName == "" {
		f.DisplayName = f.ID
	}
	return f, nil
}

// chromeOf reads a chrome object's fields by role. A role named group.field
// is read from the nested object group.
func chromeOf(m map[string]any) (raw chromeRaw) {
	for r := RoleAccent; r <= roleLastSettable; r++ {
		obj, key := m, r.String()
		if group, field, nested := strings.Cut(key, "."); nested {
			obj, _ = m[group].(map[string]any)
			key = field
		}
		raw[r], _ = obj[key].(string)
	}
	return raw
}

// chromeRawOf is a registered chrome as the fields a file would write: the
// colours the theme named, each as hex, and none of the steps it derived.
func chromeRawOf(c *Chrome) (raw chromeRaw) {
	if c == nil {
		return raw
	}
	named := [...]color.Color{
		RoleAccent: c.Accent, RoleAccentBright: c.AccentBright, RoleSuccess: c.Success,
		RoleWarning: c.Warning, RoleError: c.Error, RoleInfo: c.Info,
		RoleSurface: c.Surface, RoleCanvas: c.Canvas, RolePanel: c.Panel, RoleCard: c.Card,
	}
	for r := RoleAccent; r <= RoleCard; r++ {
		if named[r] != nil && !c.derived[r] {
			raw[r] = ColorToString(named[r])
		}
	}
	for r := RoleCard + 1; r <= roleLastSettable; r++ {
		if f := c.furniture[r]; f != nil {
			raw[r] = ColorToString(f)
		}
	}
	return raw
}

// resolvedTheme is a theme file with its base merged in: the colours a frame
// draws, and the chrome fields as strings, so a file based on this one merges
// its own chrome over them before anything is resolved.
type resolvedTheme struct {
	tint   *tint.Tint
	chrome chromeRaw
	path   string
	base   string
}

// builtins is every theme tuios ships, by id, as it was before any file in the
// themes directory was read. A file may take a built-in's id (an edited copy),
// and its base is then the built-in, not itself. customs is every file read
// from the directory, resolved.
var themeBases struct {
	sync.RWMutex
	builtins map[string]resolvedTheme
	customs  map[string]resolvedTheme
}

// rememberBuiltins records the registry as it stands before the themes
// directory is read, with each theme's chrome: a CharmTone or Omarchy base
// passes on its accents and grounds, not only its sixteen.
func rememberBuiltins() {
	m := make(map[string]resolvedTheme)
	for _, id := range tint.TintIDs() {
		if t, ok := tint.GetTint(id); ok {
			m[id] = builtinTheme(id, t)
		}
	}
	themeBases.Lock()
	themeBases.builtins = m
	themeBases.Unlock()
}

// baseTheme finds the base id for the file self: a file of the directory
// first, unless it is self, then a built-in. Before the registry is built, as
// in a test that reads one file, a built-in is whatever the registry holds.
func baseTheme(id, self string) (resolvedTheme, bool) {
	themeBases.RLock()
	defer themeBases.RUnlock()
	if r, ok := themeBases.customs[id]; ok && id != self {
		return r, true
	}
	if themeBases.builtins == nil {
		t, ok := tint.GetTint(id)
		return builtinTheme(id, t), ok
	}
	r, ok := themeBases.builtins[id]
	return r, ok
}

// builtinTheme is a registered theme and its chrome as a base.
func builtinTheme(id string, t *tint.Tint) resolvedTheme {
	chromeRegistry.RLock()
	c := chromeRegistry.byID[id]
	chromeRegistry.RUnlock()
	return resolvedTheme{tint: t, chrome: chromeRawOf(c)}
}

// mergeBase merges f over its base.
func mergeBase(f *themeFile) (resolvedTheme, error) {
	t := f.Tint
	chrome := chromeOf(f.Chrome)
	if f.Dark != nil {
		t.Dark = *f.Dark
	}
	if f.Base != "" {
		base, ok := baseTheme(f.Base, f.ID)
		if !ok {
			return resolvedTheme{}, fmt.Errorf("base %q is not a theme. Run tuios list-themes for the names", f.Base)
		}
		from := themeColours(base.tint)
		for i, c := range themeColours(&t) {
			if *c == nil {
				*c = copyColor(*from[i])
			}
		}
		if f.Dark == nil {
			t.Dark = base.tint.Dark
		}
		for r := range chrome {
			if strings.TrimSpace(chrome[r]) == "" {
				chrome[r] = base.chrome[r]
			}
		}
	}
	fillDefaults(&t)
	return resolvedTheme{tint: &t, chrome: chrome, path: f.path, base: f.Base}, nil
}

// themeColours lists a tint's colour fields, so a merge or a diff walks them
// all in one place.
func themeColours(t *tint.Tint) []**tint.Color {
	return []**tint.Color{
		&t.Fg, &t.Bg, &t.SelectionBg, &t.Cursor,
		&t.Black, &t.Red, &t.Green, &t.Yellow, &t.Blue, &t.Purple, &t.Cyan, &t.White,
		&t.BrightBlack, &t.BrightRed, &t.BrightGreen, &t.BrightYellow,
		&t.BrightBlue, &t.BrightPurple, &t.BrightCyan, &t.BrightWhite,
	}
}

// loadThemeFiles merges and registers the files of the themes directory. A
// file whose base is another file waits for that file, so the order the
// directory lists them in does not matter. A file whose base never loads, a
// missing one or a loop of files that name each other, is left out with an
// error under its id.
func loadThemeFiles(files map[string]*themeFile) (map[string]resolvedTheme, map[string]error) {
	done := make(map[string]resolvedTheme, len(files))
	failed := make(map[string]error)
	themeBases.Lock()
	themeBases.customs = done
	themeBases.Unlock()
	for progress := true; progress; {
		progress = false
		for id, f := range files {
			if _, waits := files[f.Base]; waits && f.Base != id {
				if _, loaded := done[f.Base]; !loaded {
					continue
				}
			}
			delete(files, id)
			progress = true
			if berr, bad := failed[f.Base]; bad && f.Base != id {
				failed[id] = fmt.Errorf("base %q does not load: %w", f.Base, berr)
				continue
			}
			r, err := mergeBase(f)
			if err == nil {
				err = registerResolved(r)
			}
			if err != nil {
				failed[id] = err
				continue
			}
			themeBases.Lock()
			done[id] = r
			themeBases.Unlock()
		}
	}
	for id, f := range files {
		failed[id] = fmt.Errorf("base %q does not load: a base is missing or the bases name each other in a loop", f.Base)
	}
	return done, failed
}

// registerResolved registers a resolved theme and its chrome under its id.
func registerResolved(r resolvedTheme) error {
	chrome, err := buildChrome(r.chrome, r.tint)
	if err != nil {
		return err
	}
	tint.Register(r.tint)
	registerChrome(r.tint.ID, chrome)
	return nil
}

// ThemeFileOf reports whether a theme comes from a file in the themes
// directory, and whether that file takes the id of a built-in theme, which
// makes it an edited copy of the built-in.
func ThemeFileOf(id string) (yours, editsBuiltin bool) {
	themeBases.RLock()
	defer themeBases.RUnlock()
	_, yours = themeBases.customs[id]
	_, builtin := themeBases.builtins[id]
	return yours, yours && builtin
}

// ThemesBasedOn lists the theme files that name id as their base, sorted.
func ThemesBasedOn(id string) []string {
	themeBases.RLock()
	defer themeBases.RUnlock()
	var out []string
	for child, r := range themeBases.customs {
		if r.base == id && child != id {
			out = append(out, child)
		}
	}
	slices.Sort(out)
	return out
}

// DeleteThemeFile removes the file that defines a theme of yours. For an
// edited built-in that brings the built-in back. The theme in force is not
// changed: the caller selects another first, or selects the built-in again.
func DeleteThemeFile(id string) error {
	themeBases.Lock()
	r, ok := themeBases.customs[id]
	builtin, isBuiltin := themeBases.builtins[id]
	if ok {
		delete(themeBases.customs, id)
	}
	themeBases.Unlock()
	if !ok || r.path == "" {
		return fmt.Errorf("%s is not a theme file", id)
	}
	if err := os.Remove(r.path); err != nil {
		return err
	}
	registerChrome(id, nil)
	tint.UnregisterIDs(id)
	// An edited built-in gives way to the built-in, chrome and all.
	if isBuiltin && builtin.tint != nil {
		return registerResolved(builtin)
	}
	return nil
}
