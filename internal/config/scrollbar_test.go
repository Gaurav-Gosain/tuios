package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// withScrollbarGlobals restores the scrollbar globals after a test that moves
// them, since they are package state shared with every other test in the run.
func withScrollbarGlobals(t *testing.T) {
	t.Helper()
	style, thumb, track, tint := config.ScrollbarStyle, config.ScrollbarThumb, config.ScrollbarTrack, config.ScrollbarTint
	ascii, border := config.UseASCIIOnly, config.BorderStyle
	t.Cleanup(func() {
		config.ScrollbarStyle, config.ScrollbarThumb, config.ScrollbarTrack, config.ScrollbarTint = style, thumb, track, tint
		config.UseASCIIOnly, config.BorderStyle = ascii, border
	})
}

// The glyphs are the default look, so they are what an unconfigured install
// gets: one box-drawing vertical at two weights in the thin style, a whole
// block on the surface fill in the track style, and a pipe with no track in
// ASCII.
func TestScrollbarGlyphDefaultsPerStyle(t *testing.T) {
	withScrollbarGlobals(t)
	config.ScrollbarThumb, config.ScrollbarTrack = "", ""
	config.UseASCIIOnly, config.BorderStyle = false, "rounded"

	config.ScrollbarStyle = config.ScrollbarStyleThin
	if got := config.GetScrollbarThumbChar(); got != "┃" {
		t.Errorf("thin thumb = %q, want ┃", got)
	}
	if got := config.GetScrollbarTrackChar(); got != "│" {
		t.Errorf("thin track = %q, want │", got)
	}

	config.ScrollbarStyle = config.ScrollbarStyleTrack
	if got := config.GetScrollbarThumbChar(); got != "█" {
		t.Errorf("track thumb = %q, want █", got)
	}
	if got := config.GetScrollbarTrackChar(); got != "" {
		t.Errorf("track track = %q, want the surface fill (empty)", got)
	}

	config.UseASCIIOnly = true
	config.ScrollbarStyle = config.ScrollbarStyleThin
	if got := config.GetScrollbarThumbChar(); got != "|" {
		t.Errorf("ASCII thumb = %q, want |", got)
	}
	if got := config.GetScrollbarTrackChar(); got != "" {
		t.Errorf("ASCII track = %q, want none", got)
	}
}

// A one-cell override is honoured, and none is the way back to the look the
// thin style had before it grew a track.
func TestScrollbarGlyphOverrides(t *testing.T) {
	withScrollbarGlobals(t)
	config.UseASCIIOnly, config.BorderStyle = false, "rounded"
	config.ScrollbarStyle = config.ScrollbarStyleThin

	config.ScrollbarThumb, config.ScrollbarTrack = "▕", config.ScrollbarTrackNone
	if got := config.GetScrollbarThumbChar(); got != "▕" {
		t.Errorf("configured thumb = %q, want ▕", got)
	}
	if got := config.GetScrollbarTrackChar(); got != "" {
		t.Errorf("track = none drew %q, want nothing", got)
	}

	// A unicode override cannot survive into an ASCII frame, so it defers to the
	// ASCII default rather than punching a hole in it.
	config.UseASCIIOnly = true
	if got := config.GetScrollbarThumbChar(); got != "|" {
		t.Errorf("ASCII thumb with a unicode override = %q, want |", got)
	}
}

// The bar is one column wide, so a glyph that measures anything but one cell
// would shift the content it floats over. Rejected, warned about once, and the
// style's default is drawn instead.
func TestScrollbarGlyphMustMeasureOneCell(t *testing.T) {
	withScrollbarGlobals(t)
	config.UseASCIIOnly, config.BorderStyle = false, "rounded"
	config.ScrollbarStyle = config.ScrollbarStyleThin

	cfg := config.DefaultConfig()
	cfg.Appearance.Scrollbar.Thumb = "▐▐"
	cfg.Appearance.Scrollbar.Track = "ab"
	cfg.Appearance.Scrollbar.Tint = "chartreuse"

	result := config.ValidateConfig(cfg)
	keys := map[string]int{}
	for _, w := range result.Warnings {
		keys[w.Key]++
	}
	for _, key := range []string{"scrollbar.thumb", "scrollbar.track", "scrollbar.tint"} {
		if keys[key] != 1 {
			t.Errorf("%s raised %d warnings, want exactly one", key, keys[key])
		}
	}

	config.ApplyAppearanceConfig(cfg)
	if got := config.GetScrollbarThumbChar(); got != "┃" {
		t.Errorf("a two-cell thumb was drawn as %q, want the default ┃", got)
	}
	if got := config.GetScrollbarTrackChar(); got != "│" {
		t.Errorf("a two-cell track was drawn as %q, want the default │", got)
	}
}

// The tint takes three keywords and a hex literal; anything else warns.
func TestScrollbarTintValidation(t *testing.T) {
	for _, tint := range []string{"", "quiet", "border", "muted", "#6B50FF", "#0000ee"} {
		cfg := config.DefaultConfig()
		cfg.Appearance.Scrollbar.Tint = tint
		for _, w := range config.ValidateConfig(cfg).Warnings {
			if w.Key == "scrollbar.tint" {
				t.Errorf("tint %q warned: %s", tint, w.Message)
			}
		}
	}
	withScrollbarGlobals(t)
	for _, tint := range []string{"#fff", "border ", "0000ee", "rebeccapurple"} {
		cfg := config.DefaultConfig()
		cfg.Appearance.Scrollbar.Tint = tint
		var warned bool
		for _, w := range config.ValidateConfig(cfg).Warnings {
			warned = warned || w.Key == "scrollbar.tint"
		}
		if !warned {
			t.Errorf("tint %q was accepted", tint)
		}
		// A malformed literal is refused at use as well, or the bar would be
		// drawn in no colour at all.
		config.ScrollbarTint = tint
		if hex, ok := config.ScrollbarTintHex(); ok {
			t.Errorf("tint %q resolved to the colour %q", tint, hex)
		}
	}
	config.ScrollbarTint = "#6B50FF"
	if hex, ok := config.ScrollbarTintHex(); !ok || hex != "#6B50FF" {
		t.Errorf("a valid hex resolved to %q (ok=%v)", hex, ok)
	}
}

// A config written before the table grew its keys has to load and render the
// documented defaults. A section added to the config once swallowed everything
// written after it, so the check reads the keys around the table as well.
func TestScrollbarKeysAbsentFromAnOlderConfig(t *testing.T) {
	withScrollbarGlobals(t)
	older := strings.Join([]string{
		"[appearance]",
		`border_style = "rounded"`,
		"scrollback_lines = 12345",
		"",
		"[appearance.scrollbar]",
		`style = "thin"`,
		"",
		"[appearance.sidebar]",
		"width = 31",
		"",
	}, "\n")
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(older), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.ReloadConfig(path)
	if err != nil {
		t.Fatalf("ReloadConfig: %v", err)
	}
	if cfg.Appearance.ScrollbackLines != 12345 || cfg.Appearance.Sidebar.Width != 31 {
		t.Fatalf("keys around the scrollbar table were lost: scrollback=%d sidebar width=%d",
			cfg.Appearance.ScrollbackLines, cfg.Appearance.Sidebar.Width)
	}
	if w := config.ValidateConfig(cfg).Warnings; len(w) > 0 {
		for _, warning := range w {
			t.Errorf("an older config warned about %s: %s", warning.Key, warning.Message)
		}
	}

	config.UseASCIIOnly, config.BorderStyle = false, "rounded"
	config.ApplyAppearanceConfig(cfg)
	if config.ScrollbarStyle != config.ScrollbarStyleThin || config.ScrollbarTint != config.ScrollbarTintQuiet {
		t.Errorf("older config resolved to style %q tint %q, want thin/quiet",
			config.ScrollbarStyle, config.ScrollbarTint)
	}
	if thumb, track := config.GetScrollbarThumbChar(), config.GetScrollbarTrackChar(); thumb != "┃" || track != "│" {
		t.Errorf("older config drew thumb %q track %q, want ┃ on │", thumb, track)
	}
}
