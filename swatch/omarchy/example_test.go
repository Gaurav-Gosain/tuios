package omarchy_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"tuios.dev/swatch"
	"tuios.dev/swatch/omarchy"
)

// Read the theme Omarchy has applied. On a machine without Omarchy, Load
// returns swatch.ErrNotFound.
func ExampleCurrent() {
	p, err := omarchy.Current().Load()
	if errors.Is(err, swatch.ErrNotFound) {
		return // not on Omarchy: keep the app's own colours
	}
	if err != nil {
		return
	}
	fmt.Println(p.Name, swatch.Hex(p.Accent))
}

// Read the theme applied in a given current directory, here a copy of an
// Omarchy 4 state directory with Tokyo Night applied.
func ExampleAt() {
	p, err := omarchy.At("testdata/current").Load()
	if err != nil {
		panic(err)
	}
	fmt.Println(p.Name)
	fmt.Println("dark:", p.IsDark())
	fmt.Println("background:", swatch.Hex(p.Background))
	fmt.Println("accent:", swatch.Hex(p.Accent))
	fmt.Println("red:", swatch.Hex(p.ANSI[1]))
	// Output:
	// Tokyo Night
	// dark: true
	// background: #1a1b26
	// accent: #7aa2f7
	// red: #f7768e
}

// List the installed themes, without applying one.
func ExampleThemesIn() {
	for _, t := range omarchy.ThemesIn("testdata/themes")[:3] {
		p, err := t.Load()
		if err != nil {
			continue
		}
		fmt.Printf("%s: %s, light %v\n", t.Name(), swatch.Hex(p.Accent), p.Light)
	}
	// Output:
	// Catppuccin: #89b4fa, light false
	// Catppuccin Latte: #1e66f5, light true
	// Ethereal: #7d82d9, light false
}

// Follow the applied theme. Here a theme switch is made the way
// omarchy-theme-set makes it: the new theme is staged beside the old one,
// the old one is removed, the new one renamed into its place, and the name
// written last.
func ExampleAt_watch() {
	current, err := os.MkdirTemp("", "omarchy-current")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(current)
	apply := func(id string) {
		next := filepath.Join(current, "next-theme")
		_ = os.MkdirAll(next, 0o755)
		data, _ := os.ReadFile(filepath.Join("testdata", "themes", id, "colors.toml"))
		_ = os.WriteFile(filepath.Join(next, "colors.toml"), data, 0o644)
		_ = os.RemoveAll(filepath.Join(current, "theme"))
		_ = os.Rename(next, filepath.Join(current, "theme"))
		_ = os.WriteFile(filepath.Join(current, "theme.name"), []byte(id+"\n"), 0o644)
	}
	apply("tokyo-night")

	w, err := swatch.Watch(omarchy.At(current))
	if err != nil {
		panic(err)
	}
	defer w.Close()
	p, _ := w.Current()
	fmt.Println(p.Name, p.Light)

	apply("catppuccin-latte")
	select {
	case p = <-w.Changes():
		fmt.Println(p.Name, p.Light, swatch.Hex(p.Accent))
	case <-time.After(5 * time.Second):
		fmt.Println("no change seen")
	}
	// Output:
	// Tokyo Night false
	// Catppuccin Latte true #1e66f5
}
