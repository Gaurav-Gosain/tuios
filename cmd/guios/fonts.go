package main

import (
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// kero carries JetBrains Mono, with Nerd Fonts' icons (resources/fonts,
// SIL Open Font License, see OFL.txt there): the terminals' font unless
// the user picks another, and the icons of prompts and of agents' TUIs.

// bundledFont is the family the fonts register as.
const bundledFont = "JetBrainsMono Nerd Font"

// fontsLoaded tells that the fonts registered, so that a terminal asks
// for them only where they are.
var fontsLoaded bool

// loadFonts registers the fonts kero carries: from the app's resources,
// where `mygo build` and `mygo dev` put them, else from the source tree,
// as under `go run`.
func loadFonts() {
	var dirs []string
	if res, err := mygo.App.Path(mygo.PathResources); err == nil {
		dirs = append(dirs, filepath.Join(res, "fonts"))
	}
	dirs = append(dirs, filepath.Join("resources", "fonts"))
	for _, dir := range dirs {
		files, _ := filepath.Glob(filepath.Join(dir, "*.ttf"))
		for _, f := range files {
			data, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			if ui.RegisterFont(data, bundledFont) == nil {
				fontsLoaded = true
			}
		}
		if fontsLoaded {
			return
		}
	}
}

// termFamily returns the family terminals draw with: the user's, else
// the font kero carries, else the system's monospaced one.
func (a *App) termFamily() string {
	if f := a.set.FontFamily; f != "" {
		return f
	}
	if fontsLoaded {
		return bundledFont
	}
	return "monospace"
}
