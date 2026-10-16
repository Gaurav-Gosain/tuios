package theme

import (
	"encoding/json"
	"fmt"
	"path/filepath"
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
	tint.Tint
	Base   string            `json:"base,omitempty"`
	Dark   *bool             `json:"dark,omitempty"`
	Chrome map[string]string `json:"chrome,omitempty"`
}

// readThemeFile parses a theme file's bytes. The id comes from the file name
// when the file names none.
func readThemeFile(path string, data []byte) (*themeFile, error) {
	f := &themeFile{}
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

// chromeOf reads a chrome object's fields by role.
func chromeOf(m map[string]string) (raw chromeRaw) {
	for r := RoleAccent; r <= RoleCard; r++ {
		raw[r] = m[r.String()]
	}
	return raw
}

// resolvedTheme is a theme file with its base merged in: the colours a frame
// draws, and the chrome fields as strings, so a file based on this one merges
// its own chrome over them before anything is resolved.
type resolvedTheme struct {
	tint   *tint.Tint
	chrome chromeRaw
}

// builtins is every theme tuios ships, by id, as it was before any file in the
// themes directory was read. A file may take a built-in's id (an edited copy),
// and its base is then the built-in, not itself. customs is every file read
// from the directory, resolved.
var themeBases struct {
	sync.RWMutex
	builtins map[string]*tint.Tint
	customs  map[string]resolvedTheme
}

// rememberBuiltins records the registry as it stands before the themes
// directory is read.
func rememberBuiltins() {
	m := make(map[string]*tint.Tint)
	for _, id := range tint.TintIDs() {
		if t, ok := tint.GetTint(id); ok {
			m[id] = t
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
	t, ok := themeBases.builtins[id]
	if themeBases.builtins == nil {
		t, ok = tint.GetTint(id)
	}
	return resolvedTheme{tint: t}, ok
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
	return resolvedTheme{tint: &t, chrome: chrome}, nil
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
