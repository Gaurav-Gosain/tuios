package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// backgroundOption is one background option: its registry path, the key it is
// written under in its TOML table, the table, and the funnel the renderer
// reads it through.
type backgroundOption struct {
	path, key, table string
	resolved         func(*config.Settings) string
}

// backgroundOptions is every background, the default for every surface first.
var backgroundOptions = []backgroundOption{
	{"appearance.background", "background", "appearance", (*config.Settings).AllBackgroundResolved},
	{"appearance.pane_background", "pane_background", "appearance", (*config.Settings).PaneBackgroundResolved},
	{"appearance.desktop_background", "desktop_background", "appearance", (*config.Settings).DesktopBackgroundResolved},
	{"appearance.window_chrome_background", "window_chrome_background", "appearance", (*config.Settings).WindowChromeBackgroundResolved},
	{"appearance.dock_background", "dock_background", "appearance", (*config.Settings).DockBackgroundResolved},
	{"appearance.sidebar.background", "background", "appearance.sidebar", (*config.Settings).SidebarBackgroundResolved},
}

// surfaceOptions is every background but the default for every surface.
var surfaceOptions = backgroundOptions[1:]

// backgroundWarnings is the background warnings a config raises.
func backgroundWarnings(cfg *config.UserConfig) []string {
	var out []string
	for _, w := range config.ValidateConfig(cfg).Warnings {
		if strings.HasSuffix(w.Key, "background") {
			out = append(out, w.Key+": "+w.Message)
		}
	}
	return out
}

// loadBackgrounds writes a config file holding the given tables and reads it
// back through the funnel the renderer reads.
func loadBackgrounds(t *testing.T, themeID string, tables map[string][]string) (*config.UserConfig, config.Settings) {
	t.Helper()
	appearance := []string{"[appearance]"}
	if themeID != "" {
		appearance = append(appearance, `theme = "`+themeID+`"`)
	}
	appearance = append(appearance, tables["appearance"]...)
	lines := appearance
	if sb := tables["appearance.sidebar"]; len(sb) > 0 {
		lines = append(lines, "[appearance.sidebar]")
		lines = append(lines, sb...)
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.ReloadConfig(path)
	if err != nil {
		t.Fatalf("ReloadConfig: %v", err)
	}
	s := config.DefaultSettings()
	config.ApplyAppearanceConfig(cfg, &s)
	return cfg, s
}

// TestBackgroundFromTheFile reads each background option out of a config file
// and through the funnel the renderer reads, for each shape the value can take.
func TestBackgroundFromTheFile(t *testing.T) {
	cases := []struct {
		name     string
		value    string // written as key = "value", or no line for "-"
		theme    string
		want     string
		warnings int
	}{
		{name: "absent is off", value: "-", want: config.BackgroundOff},
		{name: "off", value: "off", want: config.BackgroundOff},
		{name: "theme with a theme", value: "theme", theme: "nord", want: config.BackgroundTheme},
		{name: "theme without a theme warns", value: "theme", want: config.BackgroundTheme, warnings: 1},
		{name: "hex", value: "#1e1e2e", want: "#1e1e2e"},
		{name: "upper-case hex", value: "#1E1E2E", want: "#1E1E2E"},
		{name: "short hex is not a colour", value: "#fff", want: config.BackgroundOff, warnings: 1},
		{name: "a colour name is not a colour", value: "navy", want: config.BackgroundOff, warnings: 1},
		{name: "a hex without the hash is not a colour", value: "1e1e2e", want: config.BackgroundOff, warnings: 1},
	}
	for _, opt := range backgroundOptions {
		for _, tc := range cases {
			t.Run(opt.path+"/"+tc.name, func(t *testing.T) {
				tables := map[string][]string{}
				if tc.value != "-" {
					tables[opt.table] = []string{opt.key + ` = "` + tc.value + `"`}
				}
				cfg, s := loadBackgrounds(t, tc.theme, tables)
				if got := backgroundWarnings(cfg); len(got) != tc.warnings {
					t.Errorf("%d warnings, want %d: %q", len(got), tc.warnings, got)
				}
				if got := opt.resolved(&s); got != tc.want {
					t.Errorf("resolved to %q, want %q", got, tc.want)
				}
			})
		}
	}
}

// TestBackgroundPrecedence is the rule between appearance.background and each
// surface's own option: the surface's own value wins whenever it holds one,
// off included, and empty follows the default for every surface.
func TestBackgroundPrecedence(t *testing.T) {
	cases := []struct {
		name     string
		all, own string
		want     string
	}{
		{name: "both unset", want: config.BackgroundOff},
		{name: "all off", all: "off", want: config.BackgroundOff},
		{name: "all theme reaches an unset surface", all: "theme", want: config.BackgroundTheme},
		{name: "all colour reaches an unset surface", all: "#101010", want: "#101010"},
		{name: "own colour overrides all", all: "theme", own: "#202020", want: "#202020"},
		{name: "own theme overrides an all colour", all: "#101010", own: "theme", want: config.BackgroundTheme},
		{name: "own off keeps one surface bare", all: "#101010", own: "off", want: config.BackgroundOff},
		{name: "own colour with all off", all: "off", own: "#202020", want: "#202020"},
		{name: "own typo is off, not all", all: "#101010", own: "navy", want: config.BackgroundOff},
		{name: "all typo is off", all: "navy", want: config.BackgroundOff},
	}
	for _, tc := range cases {
		if got := config.ResolveBackground(tc.own, tc.all); got != tc.want {
			t.Errorf("%s: ResolveBackground(%q, %q) = %q, want %q", tc.name, tc.own, tc.all, got, tc.want)
		}
	}
	// And through the file, for every surface: one line paints everything,
	// and a surface's own line overrides it without touching the others.
	for _, opt := range surfaceOptions {
		t.Run(opt.path, func(t *testing.T) {
			tables := map[string][]string{"appearance": {`background = "#101010"`}}
			tables[opt.table] = append(tables[opt.table], opt.key+` = "#202020"`)
			_, s := loadBackgrounds(t, "", tables)
			if got := opt.resolved(&s); got != "#202020" {
				t.Errorf("the surface's own value resolved to %q, want #202020", got)
			}
			for _, other := range surfaceOptions {
				if other.path == opt.path {
					continue
				}
				if got := other.resolved(&s); got != "#101010" {
					t.Errorf("%s resolved to %q, want the default for every surface #101010", other.path, got)
				}
			}
		})
	}
}

// TestBackgroundDefaultsAgree pins the default in every place that states
// one, since the registry, DefaultConfig and DefaultSettings are written
// separately and a disagreement shows as a settings row that reads one value
// while the screen draws another. The default for every surface is off, and
// each surface's own option is empty so that it follows it.
func TestBackgroundDefaultsAgree(t *testing.T) {
	for _, opt := range backgroundOptions {
		want := ""
		if opt.path == "appearance.background" {
			want = config.BackgroundOff
		}
		reg, ok := config.LookupOption(opt.path)
		if !ok {
			t.Fatalf("%s is not in the registry", opt.path)
		}
		if reg.Default != want {
			t.Errorf("%s: registry default %q, want %q", opt.path, reg.Default, want)
		}
		if !reg.Color || reg.Type != config.OptionString {
			t.Errorf("%s: registry entry %+v, want a colour string", opt.path, reg)
		}
		if got, _ := config.GetOptionValue(config.DefaultConfig(), opt.path); got != want {
			t.Errorf("%s: get-option on a default config reads %q, want %q", opt.path, got, want)
		}
		s := config.DefaultSettings()
		if got := opt.resolved(&s); got != config.BackgroundOff {
			t.Errorf("%s: DefaultSettings resolves to %q, want off", opt.path, got)
		}
	}
}

// TestSetOptionBackground is the live path: set-option and the settings page
// both write through SetOptionValue, which has to take the keywords and a
// colour and refuse anything else before it reaches the config.
func TestSetOptionBackground(t *testing.T) {
	cases := []struct {
		value string
		ok    bool
	}{
		{"off", true},
		{"theme", true},
		{"#282a36", true},
		{"", true}, // unset
		{"auto", false},
		{"#28a", false},
		{"red", false},
		{"theme ", false},
	}
	for _, opt := range backgroundOptions {
		for _, tc := range cases {
			t.Run(opt.path+"/"+tc.value, func(t *testing.T) {
				cfg := config.DefaultConfig()
				before, _ := config.GetOptionValue(cfg, opt.path)
				err := config.SetOptionValue(cfg, opt.path, tc.value)
				if (err == nil) != tc.ok {
					t.Fatalf("SetOptionValue(%q) error = %v, want ok=%v", tc.value, err, tc.ok)
				}
				if !tc.ok {
					if !strings.Contains(err.Error(), "#RRGGBB") {
						t.Errorf("the refusal does not say what a colour looks like: %v", err)
					}
					if got, _ := config.GetOptionValue(cfg, opt.path); got != before {
						t.Errorf("a refused value was written: %q", got)
					}
					return
				}
				if got, _ := config.GetOptionValue(cfg, opt.path); got != tc.value {
					t.Errorf("read back %q, want %q", got, tc.value)
				}
			})
		}
	}
}

// TestBackgroundSurvivesSave writes a config holding every background and
// reads it back, so an unset surface stays unset rather than being pinned to
// off by the save, which would stop it following appearance.background.
func TestBackgroundSurvivesSave(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Appearance.Background = "#101010"
	cfg.Appearance.DockBackground = "off"
	cfg.Appearance.Sidebar.Background = "theme"
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := config.WriteConfigFile(cfg, path); err != nil {
		t.Fatalf("save: %v", err)
	}
	back, err := config.ReloadConfig(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	s := config.DefaultSettings()
	config.ApplyAppearanceConfig(back, &s)
	for _, c := range []struct {
		name, got, want string
	}{
		{"pane", s.PaneBackgroundResolved(), "#101010"},
		{"desktop", s.DesktopBackgroundResolved(), "#101010"},
		{"chrome", s.WindowChromeBackgroundResolved(), "#101010"},
		{"dock", s.DockBackgroundResolved(), config.BackgroundOff},
		{"sidebar", s.SidebarBackgroundResolved(), config.BackgroundTheme},
	} {
		if c.got != c.want {
			t.Errorf("%s resolved to %q after a save, want %q", c.name, c.got, c.want)
		}
	}
}
