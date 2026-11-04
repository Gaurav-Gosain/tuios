package main

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// guios carries JetBrains Mono, with Nerd Fonts' icons in its Mono build,
// whose icons take one cell (resources/fonts, SIL Open Font License, see
// OFL.txt there): the terminals' font unless the user picks another, and
// the icons of prompts and of agents' TUIs.

// bundledFont is the family the fonts register as.
const bundledFont = "JetBrainsMono Nerd Font Mono"

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
		// The Mono build alone: the others draw icons wider than a cell.
		files, _ := filepath.Glob(filepath.Join(dir, "*NerdFontMono-*.ttf"))
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

// termFamily returns the family terminals draw with: the user's first
// installed monospaced family, else the font guios carries, else the
// system's monospaced one. A family that is not installed would make the
// text system fall back to a proportional font, which breaks the grid.
func (a *App) termFamily() string {
	if f := usableFamily(a.set.FontFamily); f != "" {
		return f
	}
	if fontsLoaded {
		return bundledFont
	}
	return "monospace"
}

// usableFamily is the first family of a comma-separated list that is
// installed and monospaced, "" for none.
func usableFamily(list string) string {
	for f := range strings.SplitSeq(list, ",") {
		f = strings.TrimSpace(f)
		switch {
		case f == "":
		case f == "monospace":
			return f
		case strings.EqualFold(f, bundledFont):
			if fontsLoaded {
				return bundledFont
			}
		case monoFamilies()[strings.ToLower(f)]:
			return f
		}
	}
	return ""
}

// monoFamilies are the monospaced families installed, by lower-case
// name. The system lists them once (tens of milliseconds with many
// fonts), and only when the user picked a font of their own.
var monoFamilies = sync.OnceValue(func() map[string]bool {
	m := map[string]bool{}
	for _, f := range ui.FontFamilies() {
		if f.Monospaced {
			m[strings.ToLower(f.Name)] = true
		}
	}
	return m
})
