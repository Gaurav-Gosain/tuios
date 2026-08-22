package theme

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/adrg/xdg"
	tint "github.com/lrstanley/bubbletint/v2"
)

// GetThemesDir returns the path to the custom themes directory (~/.config/tuios/themes/).
// Creates the directory if it doesn't exist.
func GetThemesDir() (string, error) {
	// Use xdg.ConfigFile to get the path and ensure parent dirs exist
	keepFile, err := xdg.ConfigFile("tuios/themes/.keep")
	if err != nil {
		return "", fmt.Errorf("failed to get themes directory: %w", err)
	}
	return filepath.Dir(keepFile), nil
}

// LoadCustomThemes reads all *.json files from the themes directory,
// loads each as a custom theme, and registers them with bubbletint.
// Returns the list of successfully loaded theme IDs.
// Logs warnings for bad files but doesn't fail startup.
func LoadCustomThemes(themesDir string) ([]string, error) {
	loaded, problems, err := readCustomThemes(themesDir)
	for _, p := range problems {
		log.Printf("Warning: %s", p)
	}
	return loaded, err
}

// readCustomThemes is LoadCustomThemes with the bad files handed back instead
// of logged.
//
// A log line is the right place for this at startup and the wrong place for a
// caller that has to answer for the file: an agent writing a palette and
// applying it needs to be told which file is malformed and how, and it cannot
// read the daemon's log. Both callers share the scan so the two answers cannot
// drift.
func readCustomThemes(themesDir string) (loaded, problems []string, err error) {
	entries, err := os.ReadDir(themesDir)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read themes directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			continue
		}

		path := filepath.Join(themesDir, entry.Name())
		t, err := LoadCustomThemeFile(path)
		if err != nil {
			problems = append(problems, fmt.Sprintf("skipping custom theme %s: %v", entry.Name(), err))
			continue
		}

		tint.Register(t)
		loaded = append(loaded, t.ID)
	}

	return loaded, problems, nil
}

// ReloadCustomThemes re-reads the themes directory and registers what it finds,
// replacing any theme already registered under the same id.
//
// The registry is built once per process behind a sync.Once, which is right for
// the built-ins and wrong for these. A custom theme file is written while tuios
// is running, by a person editing it or by an agent ricing on their behalf, and
// until this existed the only way to reach a palette you had just authored was
// to restart: selecting it reported success, logged that it did not exist, and
// went on drawing the old one.
//
// The problems it returns are one line per file it could not read. They are the
// answer to "why did my theme not turn up", which is otherwise unobtainable
// from outside the process.
func ReloadCustomThemes() (loaded, problems []string) {
	EnsureRegistry()
	dir, err := GetThemesDir()
	if err != nil {
		return nil, []string{err.Error()}
	}
	loaded, problems, err = readCustomThemes(dir)
	if err != nil {
		problems = append(problems, err.Error())
	}
	return loaded, problems
}

// Exists reports whether id names a registered theme, re-reading the themes
// directory once before it answers no.
//
// The re-read is what makes "write the file, then select it" one round trip
// rather than two and a restart. It costs a directory scan only on the path
// that is about to fail anyway.
func Exists(id string) bool {
	if id == "" {
		// Empty is the no-theme state, not a name that has to resolve.
		return true
	}
	EnsureRegistry()
	if _, ok := tint.GetTint(id); ok {
		return true
	}
	ReloadCustomThemes()
	_, ok := tint.GetTint(id)
	return ok
}

// LoadCustomThemeFile reads a JSON file and returns a *tint.Tint.
// Derives ID from filename if the id field is empty.
// Sets DisplayName from ID if empty. Fills missing color fields with defaults.
func LoadCustomThemeFile(path string) (*tint.Tint, error) {
	// #nosec G304 - path is from user's config directory, reading custom themes is intentional
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read theme file: %w", err)
	}

	var t tint.Tint
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("failed to parse theme JSON: %w", err)
	}

	// Derive ID from filename if not set in JSON
	if t.ID == "" {
		base := filepath.Base(path)
		t.ID = strings.ToLower(strings.TrimSuffix(base, filepath.Ext(base)))
	}

	if t.ID == "" {
		return nil, fmt.Errorf("theme has no ID")
	}

	// Set DisplayName from ID if empty
	if t.DisplayName == "" {
		t.DisplayName = t.ID
	}

	fillDefaults(&t)

	return &t, nil
}

// fillDefaults fills nil color pointers with xterm defaults.
func fillDefaults(t *tint.Tint) {
	// Default foreground/background
	if t.Fg == nil {
		t.Fg = tint.FromHex("#e5e5e5")
	}
	if t.Bg == nil {
		t.Bg = tint.FromHex("#000000")
	}

	// Cursor defaults to Fg
	if t.Cursor == nil {
		t.Cursor = copyColor(t.Fg)
	}

	// Normal ANSI colors (xterm defaults)
	if t.Black == nil {
		t.Black = tint.FromHex("#000000")
	}
	if t.Red == nil {
		t.Red = tint.FromHex("#cd0000")
	}
	if t.Green == nil {
		t.Green = tint.FromHex("#00cd00")
	}
	if t.Yellow == nil {
		t.Yellow = tint.FromHex("#cdcd00")
	}
	if t.Blue == nil {
		t.Blue = tint.FromHex("#0000ee")
	}
	if t.Purple == nil {
		t.Purple = tint.FromHex("#cd00cd")
	}
	if t.Cyan == nil {
		t.Cyan = tint.FromHex("#00cdcd")
	}
	if t.White == nil {
		t.White = tint.FromHex("#e5e5e5")
	}

	// Bright variants default to normal if nil
	if t.BrightBlack == nil {
		t.BrightBlack = copyColor(t.Black)
	}
	if t.BrightRed == nil {
		t.BrightRed = copyColor(t.Red)
	}
	if t.BrightGreen == nil {
		t.BrightGreen = copyColor(t.Green)
	}
	if t.BrightYellow == nil {
		t.BrightYellow = copyColor(t.Yellow)
	}
	if t.BrightBlue == nil {
		t.BrightBlue = copyColor(t.Blue)
	}
	if t.BrightPurple == nil {
		t.BrightPurple = copyColor(t.Purple)
	}
	if t.BrightCyan == nil {
		t.BrightCyan = copyColor(t.Cyan)
	}
	if t.BrightWhite == nil {
		t.BrightWhite = copyColor(t.White)
	}
}

// copyColor creates a copy of a tint.Color.
func copyColor(c *tint.Color) *tint.Color {
	if c == nil {
		return nil
	}
	dup := *c
	return &dup
}
