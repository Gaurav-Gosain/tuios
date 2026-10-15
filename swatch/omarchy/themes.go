package omarchy

import (
	"os"
	"path/filepath"
	"slices"

	"tuios.dev/swatch"
)

// Theme is one installed Omarchy theme.
type Theme struct {
	// ID is the theme's directory name, such as "tokyo-night".
	ID string
	// Dir is the directory whose colors.toml is the theme's palette: the
	// user's own copy when there is one, the shipped one otherwise.
	Dir string
}

// Name is the name Omarchy shows for the theme.
func (t Theme) Name() string { return DisplayName(t.ID) }

// Load reads the theme's palette.
func (t Theme) Load() (swatch.Palette, error) { return load(t.Dir, t.ID) }

// ThemeDirs returns the directories Omarchy installs themes in, the user's
// first: ~/.config/omarchy/themes, then $OMARCHY_PATH/themes, then the
// Omarchy 4 and the Omarchy 3 default for $OMARCHY_PATH.
func ThemeDirs(home string) []string {
	dirs := []string{filepath.Join(home, ".config", "omarchy", "themes")}
	if p := os.Getenv("OMARCHY_PATH"); p != "" {
		dirs = append(dirs, filepath.Join(p, "themes"))
	}
	return append(dirs,
		filepath.Join("/usr", "share", "omarchy", "themes"),
		filepath.Join(home, ".local", "share", "omarchy", "themes"))
}

// Themes lists the installed themes that have a colors.toml, sorted by ID.
// A user's theme overlays a shipped theme of the same name, as it does in
// Omarchy, so its colors.toml wins.
func Themes() []Theme {
	home, _ := os.UserHomeDir()
	return ThemesIn(ThemeDirs(home)...)
}

// ThemesIn is Themes over the given theme directories, the first winning.
func ThemesIn(dirs ...string) []Theme {
	seen := map[string]bool{}
	var out []Theme
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			id := e.Name()
			if seen[id] || id == "" || id[0] == '.' {
				continue
			}
			themeDir := filepath.Join(dir, id)
			info, err := os.Stat(filepath.Join(themeDir, "colors.toml"))
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			seen[id] = true
			out = append(out, Theme{ID: id, Dir: themeDir})
		}
	}
	slices.SortFunc(out, func(a, b Theme) int {
		switch {
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		}
		return 0
	})
	return out
}
