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
	style, thumb, track, tint := config.Global.ScrollbarStyle, config.Global.ScrollbarThumb, config.Global.ScrollbarTrack, config.Global.ScrollbarTint
	ascii, border := config.Global.UseASCIIOnly, config.Global.BorderStyle
	t.Cleanup(func() {
		config.Global.ScrollbarStyle, config.Global.ScrollbarThumb, config.Global.ScrollbarTrack, config.Global.ScrollbarTint = style, thumb, track, tint
		config.Global.UseASCIIOnly, config.Global.BorderStyle = ascii, border
	})
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

	config.Global.UseASCIIOnly, config.Global.BorderStyle = false, "rounded"
	config.ApplyAppearanceConfig(cfg, &config.Global)
	if config.Global.ScrollbarStyle != config.ScrollbarStyleThin || config.Global.ScrollbarTint != config.ScrollbarTintQuiet {
		t.Errorf("older config resolved to style %q tint %q, want thin/quiet",
			config.Global.ScrollbarStyle, config.Global.ScrollbarTint)
	}
	if thumb, track := config.Global.GetScrollbarThumbChar(), config.Global.GetScrollbarTrackChar(); thumb != "┃" || track != "│" {
		t.Errorf("older config drew thumb %q track %q, want ┃ on │", thumb, track)
	}
}
