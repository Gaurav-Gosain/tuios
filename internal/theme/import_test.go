package theme

import (
	"os"
	"path/filepath"
	"testing"
)

// The four formats say the same twenty things in four punctuations. Each sample
// below is the same gruvbox-ish palette written the way that terminal writes
// it, so a parser that puts a colour in the wrong slot fails on the comparison
// rather than on the count.
func TestImportReadsEachFormat(t *testing.T) {
	tests := []struct {
		name   string
		file   string
		format ImportFormat
		body   string
	}{
		{
			name:   "kitty",
			file:   "mine.conf",
			format: FormatKitty,
			body: `# gruvbox dark
foreground #ebdbb2
background #282828
cursor     #ebdbb2

color0 #282828
color1 #cc241d
color2 #98971a
color9 #fb4934
color15 #ebdbb2
`,
		},
		{
			name:   "ghostty",
			file:   "config",
			format: FormatGhostty,
			body: `# ghostty
foreground = #ebdbb2
background = #282828
cursor-color = #ebdbb2
palette = 0=#282828
palette = 1=#cc241d
palette = 2=#98971a
palette = 9=#fb4934
palette = 15=#ebdbb2
font-family = JetBrains Mono
`,
		},
		{
			name:   "alacritty",
			file:   "colors.toml",
			format: FormatAlacritty,
			body: `[colors.primary]
foreground = "#ebdbb2"
background = "#282828"

[colors.cursor]
cursor = "#ebdbb2"

[colors.normal]
black = "#282828"
red = "#cc241d"
green = "#98971a"

[colors.bright]
red = "#fb4934"
white = "#ebdbb2"
`,
		},
		{
			name:   "wezterm",
			file:   "scheme.toml",
			format: FormatWezterm,
			body: `[colors]
foreground = "#ebdbb2"
background = "#282828"
cursor_bg = "#ebdbb2"
ansi = ["#282828", "#cc241d", "#98971a", "#d79921", "#458588", "#b16286", "#689d6a", "#a89984"]
brights = ["#928374", "#fb4934", "#b8bb26", "#fabd2f", "#83a598", "#d3869b", "#8ec07c", "#ebdbb2"]
`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, tt.file)
			if err := os.WriteFile(path, []byte(tt.body), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}

			got, format, err := Import(path, "imported_"+tt.name)
			if err != nil {
				t.Fatalf("Import = %v", err)
			}
			if format != tt.format {
				t.Errorf("read as %s, want %s", format, tt.format)
			}

			// The slots every sample fills, checked by name so a colour landing
			// in the wrong one is caught rather than counted.
			for _, want := range []struct {
				name string
				hex  string
				got  string
			}{
				{"fg", "#ebdbb2", ColorToString(got.Fg)},
				{"bg", "#282828", ColorToString(got.Bg)},
				{"cursor", "#ebdbb2", ColorToString(got.Cursor)},
				{"black", "#282828", ColorToString(got.Black)},
				{"red", "#cc241d", ColorToString(got.Red)},
				{"green", "#98971a", ColorToString(got.Green)},
				{"bright_red", "#fb4934", ColorToString(got.BrightRed)},
				{"bright_white", "#ebdbb2", ColorToString(got.BrightWhite)},
			} {
				if want.got != want.hex {
					t.Errorf("%s is %s, want %s", want.name, want.got, want.hex)
				}
			}

			if !got.Dark {
				t.Error("a #282828 background was read as light")
			}
		})
	}
}

// A file that is not a colour scheme has to say so rather than import as an
// empty theme, which would apply and look like everything went wrong at once.
func TestImportRejectsSomethingElse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(path, []byte("shopping list\nmilk\neggs\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, _, err := Import(path, ""); err == nil {
		t.Error("a shopping list imported as a theme")
	}
}

// A partial scheme is a real thing to be handed: kitty configs routinely set
// only a handful. The rest fall back rather than importing as nothing.
func TestImportFillsWhatASchemeOmits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "partial.conf")
	if err := os.WriteFile(path, []byte("background #ffffff\nforeground #000000\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, _, err := Import(path, "partial")
	if err != nil {
		t.Fatalf("Import = %v", err)
	}
	if got.Blue == nil {
		t.Error("an unset colour came back nil rather than filled")
	}
	if got.Dark {
		t.Error("a #ffffff background was read as dark")
	}
}

// The round trip is what the workflow actually is: import a scheme, write it,
// select it. Describe reading the colours back is the proof it survived.
func TestImportedThemeRoundTrips(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rt.conf")
	if err := os.WriteFile(path, []byte("background #282828\nforeground #ebdbb2\ncolor1 #cc241d\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	imported, _, err := Import(path, "roundtrip_theme")
	if err != nil {
		t.Fatalf("Import = %v", err)
	}
	written, err := WriteTheme(imported)
	if err != nil {
		t.Fatalf("WriteTheme = %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(written) })

	pal, ok := Describe("roundtrip_theme")
	if !ok {
		t.Fatal("the written theme does not describe")
	}
	if pal.Bg != "#282828" {
		t.Errorf("background round-tripped as %s", pal.Bg)
	}
	for _, s := range pal.Swatches {
		if s.Name == "red" && s.Hex != "#cc241d" {
			t.Errorf("red round-tripped as %s", s.Hex)
		}
	}
}
