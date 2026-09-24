package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// paneBackgroundWarnings is the pane_background warnings a config raises.
func paneBackgroundWarnings(cfg *config.UserConfig) []string {
	var out []string
	for _, w := range config.ValidateConfig(cfg).Warnings {
		if w.Key == "pane_background" {
			out = append(out, w.Message)
		}
	}
	return out
}

// TestPaneBackgroundFromTheFile reads the option out of a config file and
// through the funnel the renderer reads, for each shape the value can take.
func TestPaneBackgroundFromTheFile(t *testing.T) {
	cases := []struct {
		name     string
		line     string // the pane_background line, or empty for none
		theme    string
		want     string // Settings.PaneBackgroundResolved
		wantHex  string
		warnings int
	}{
		{name: "absent is off", line: "", want: config.PaneBackgroundOff},
		{name: "off", line: `pane_background = "off"`, want: config.PaneBackgroundOff},
		{name: "theme with a theme", line: `pane_background = "theme"`, theme: "nord", want: config.PaneBackgroundTheme},
		{name: "theme without a theme warns", line: `pane_background = "theme"`, want: config.PaneBackgroundTheme, warnings: 1},
		{name: "hex", line: `pane_background = "#1e1e2e"`, want: "#1e1e2e", wantHex: "#1e1e2e"},
		{name: "upper-case hex", line: `pane_background = "#1E1E2E"`, want: "#1E1E2E", wantHex: "#1E1E2E"},
		{name: "short hex is not a colour", line: `pane_background = "#fff"`, want: config.PaneBackgroundOff, warnings: 1},
		{name: "a colour name is not a colour", line: `pane_background = "navy"`, want: config.PaneBackgroundOff, warnings: 1},
		{name: "a hex without the hash is not a colour", line: `pane_background = "1e1e2e"`, want: config.PaneBackgroundOff, warnings: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lines := []string{"[appearance]"}
			if tc.theme != "" {
				lines = append(lines, `theme = "`+tc.theme+`"`)
			}
			if tc.line != "" {
				lines = append(lines, tc.line)
			}
			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			cfg, err := config.ReloadConfig(path)
			if err != nil {
				t.Fatalf("ReloadConfig: %v", err)
			}
			if got := paneBackgroundWarnings(cfg); len(got) != tc.warnings {
				t.Errorf("%d warnings, want %d: %q", len(got), tc.warnings, got)
			}

			s := config.DefaultSettings()
			config.ApplyAppearanceConfig(cfg, &s)
			if got := s.PaneBackgroundResolved(); got != tc.want {
				t.Errorf("resolved to %q, want %q", got, tc.want)
			}
			hex, ok := s.PaneBackgroundHex()
			if ok != (tc.wantHex != "") || hex != tc.wantHex {
				t.Errorf("hex = %q (ok=%v), want %q", hex, ok, tc.wantHex)
			}
		})
	}
}

// TestPaneBackgroundDefaultsAgree pins the default in every place that states
// one, since the registry, DefaultConfig and DefaultSettings are written
// separately and a disagreement shows as a settings row that reads one value
// while the panes draw another.
func TestPaneBackgroundDefaultsAgree(t *testing.T) {
	opt, ok := config.LookupOption("appearance.pane_background")
	if !ok {
		t.Fatal("appearance.pane_background is not in the registry")
	}
	if opt.Default != config.PaneBackgroundOff {
		t.Errorf("registry default %q, want off", opt.Default)
	}
	if !opt.Color || opt.Type != config.OptionString || opt.Section != "appearance" {
		t.Errorf("registry entry %+v, want a colour string in appearance", opt)
	}
	if got := config.DefaultConfig().Appearance.PaneBackground; got != config.PaneBackgroundOff {
		t.Errorf("DefaultConfig holds %q, want off", got)
	}
	if got := config.DefaultSettings().PaneBackground; got != config.PaneBackgroundOff {
		t.Errorf("DefaultSettings holds %q, want off", got)
	}
	if got, _ := config.GetOptionValue(config.DefaultConfig(), "appearance.pane_background"); got != config.PaneBackgroundOff {
		t.Errorf("get-option on a default config reads %q, want off", got)
	}
}

// TestSetOptionPaneBackground is the live path: set-option and the settings
// page both write through SetOptionValue, which has to take the keywords and a
// colour and refuse anything else before it reaches the config.
func TestSetOptionPaneBackground(t *testing.T) {
	cases := []struct {
		value string
		ok    bool
	}{
		{"off", true},
		{"theme", true},
		{"#282a36", true},
		{"", true}, // unset, which resolves to off
		{"auto", false},
		{"#28a", false},
		{"red", false},
		{"theme ", false},
	}
	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			cfg := config.DefaultConfig()
			err := config.SetOptionValue(cfg, "appearance.pane_background", tc.value)
			if (err == nil) != tc.ok {
				t.Fatalf("SetOptionValue(%q) error = %v, want ok=%v", tc.value, err, tc.ok)
			}
			if !tc.ok {
				if !strings.Contains(err.Error(), "#RRGGBB") {
					t.Errorf("the refusal does not say what a colour looks like: %v", err)
				}
				if cfg.Appearance.PaneBackground != config.PaneBackgroundOff {
					t.Errorf("a refused value was written: %q", cfg.Appearance.PaneBackground)
				}
				return
			}
			if got, _ := config.GetOptionValue(cfg, "appearance.pane_background"); got != tc.value {
				t.Errorf("read back %q, want %q", got, tc.value)
			}
		})
	}
}
