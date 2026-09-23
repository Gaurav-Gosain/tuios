package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/adrg/xdg"
)

// v080Looks is the part of Settings the v0.8.0 defaults moved, read the way
// the renderer and the mouse path read it.
type v080Looks struct {
	SidebarEnabled      bool
	SidebarPosition     string
	SidebarWidth        int
	DockbarPosition     string
	WindowTitlePosition string
	ZoomSize            int
	ClickToType         string
	ScrollbarStyle      string
}

func looksOf(s config.Settings) v080Looks {
	return v080Looks{
		SidebarEnabled:      s.SidebarEnabled,
		SidebarPosition:     s.SidebarPosition,
		SidebarWidth:        s.SidebarWidth,
		DockbarPosition:     s.DockbarPosition,
		WindowTitlePosition: s.WindowTitlePosition,
		ZoomSize:            s.GetZoomSize(),
		ClickToType:         s.ClickToType,
		ScrollbarStyle:      s.ScrollbarStyle,
	}
}

// newLooks are the v0.8.0 defaults. oldLooks are the ones before it, which a
// config that writes them out keeps.
var (
	newLooks = v080Looks{
		SidebarEnabled:      true,
		SidebarPosition:     "right",
		SidebarWidth:        24,
		DockbarPosition:     "top",
		WindowTitlePosition: "top",
		ZoomSize:            95,
		ClickToType:         config.ClickToTypeDouble,
		ScrollbarStyle:      config.ScrollbarStyleTrack,
	}
	oldLooks = v080Looks{
		SidebarEnabled:      false,
		SidebarPosition:     "left",
		SidebarWidth:        28,
		DockbarPosition:     "bottom",
		WindowTitlePosition: "bottom",
		ZoomSize:            100,
		ClickToType:         config.ClickToTypeSingle,
		ScrollbarStyle:      config.ScrollbarStyleThin,
	}
)

// oldLooksTOML is a config file that sets every one of those keys to its
// pre-v0.8.0 value, which is what a first-run file written by an older
// release carries for most of them.
const oldLooksTOML = `[appearance]
dockbar_position = "bottom"
window_title_position = "bottom"
zoom_size = 100
click_to_type = "single"

[appearance.scrollbar]
style = "thin"

[appearance.sidebar]
enabled = false
position = "left"
width = 28
`

// loadLooks loads src through LoadUserConfig and seeds Settings from it the
// way startup does. A nil src means there is no config file at all.
func loadLooks(t *testing.T, src *string) v080Looks {
	t.Helper()
	dir := t.TempDir()
	t.Cleanup(xdg.Reload)
	t.Setenv("XDG_CONFIG_HOME", dir)
	xdg.Reload()
	path := filepath.Join(dir, "tuios", "config.toml")
	if src != nil {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(*src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := config.LoadUserConfig()
	if err != nil {
		t.Fatalf("LoadUserConfig: %v", err)
	}
	return looksOf(config.AppearanceFrom(cfg, config.Overrides{}))
}

// TestV080Defaults pins the seven defaults v0.8.0 changed, on every path a
// session can get its settings from, and checks that a config naming the old
// values keeps them.
func TestV080Defaults(t *testing.T) {
	str := func(s string) *string { return &s }
	cases := []struct {
		name string
		src  *string
		want v080Looks
	}{
		{"fresh install, no config file", nil, newLooks},
		{"empty config file", str(""), newLooks},
		{"empty appearance table", str("[appearance]\n"), newLooks},
		{"empty sidebar and scrollbar tables", str("[appearance.sidebar]\n[appearance.scrollbar]\n"), newLooks},
		{"unrelated keys only", str("[appearance]\nborder_style = \"thick\"\n"), newLooks},
		{"zero zoom size", str("[appearance]\nzoom_size = 0\n"), newLooks},
		{"empty title position", str("[appearance]\nwindow_title_position = \"\"\n"), newLooks},
		{"every key set to the old value", str(oldLooksTOML), oldLooks},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := loadLooks(t, c.src); got != c.want {
				t.Errorf("looks = %+v\nwant    %+v", got, c.want)
			}
		})
	}

	t.Run("DefaultSettings", func(t *testing.T) {
		if got := looksOf(config.DefaultSettings()); got != newLooks {
			t.Errorf("looks = %+v\nwant    %+v", got, newLooks)
		}
	})
	t.Run("PinPreV080Appearance", func(t *testing.T) {
		cfg := config.DefaultConfig()
		config.PinPreV080Appearance(&cfg.Appearance)
		if got := looksOf(config.AppearanceFrom(cfg, config.Overrides{})); got != oldLooks {
			t.Errorf("looks = %+v\nwant    %+v", got, oldLooks)
		}
	})
	t.Run("DefaultConfig", func(t *testing.T) {
		if got := looksOf(config.AppearanceFrom(config.DefaultConfig(), config.Overrides{})); got != newLooks {
			t.Errorf("looks = %+v\nwant    %+v", got, newLooks)
		}
	})
}

// TestV080DefaultsSetOneByOne checks each old value is kept on its own, so a
// user who wrote one key keeps that one and takes the new default for the
// rest.
func TestV080DefaultsSetOneByOne(t *testing.T) {
	cases := []struct {
		name string
		src  string
		pick func(v080Looks) any
		want any
	}{
		{"sidebar enabled", "[appearance.sidebar]\nenabled = false\n", func(l v080Looks) any { return l.SidebarEnabled }, false},
		{"sidebar position", "[appearance.sidebar]\nposition = \"left\"\n", func(l v080Looks) any { return l.SidebarPosition }, "left"},
		{"sidebar width", "[appearance.sidebar]\nwidth = 28\n", func(l v080Looks) any { return l.SidebarWidth }, 28},
		{"legacy flat sidebar key", "[appearance]\nsidebar_enabled = false\n", func(l v080Looks) any { return l.SidebarEnabled }, false},
		{"dockbar position", "[appearance]\ndockbar_position = \"bottom\"\n", func(l v080Looks) any { return l.DockbarPosition }, "bottom"},
		{"window title position", "[appearance]\nwindow_title_position = \"bottom\"\n", func(l v080Looks) any { return l.WindowTitlePosition }, "bottom"},
		{"zoom size", "[appearance]\nzoom_size = 100\n", func(l v080Looks) any { return l.ZoomSize }, 100},
		{"click to type", "[appearance]\nclick_to_type = \"single\"\n", func(l v080Looks) any { return l.ClickToType }, config.ClickToTypeSingle},
		{"scrollbar style", "[appearance.scrollbar]\nstyle = \"thin\"\n", func(l v080Looks) any { return l.ScrollbarStyle }, config.ScrollbarStyleThin},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := loadLooks(t, &c.src)
			if v := c.pick(got); v != c.want {
				t.Errorf("value = %v, want %v", v, c.want)
			}
			// Everything the file did not name is the new default.
			rest := got
			switch c.name {
			case "sidebar enabled", "legacy flat sidebar key":
				rest.SidebarEnabled = newLooks.SidebarEnabled
			case "sidebar position":
				rest.SidebarPosition = newLooks.SidebarPosition
			case "sidebar width":
				rest.SidebarWidth = newLooks.SidebarWidth
			case "dockbar position":
				rest.DockbarPosition = newLooks.DockbarPosition
			case "window title position":
				rest.WindowTitlePosition = newLooks.WindowTitlePosition
			case "zoom size":
				rest.ZoomSize = newLooks.ZoomSize
			case "click to type":
				rest.ClickToType = newLooks.ClickToType
			case "scrollbar style":
				rest.ScrollbarStyle = newLooks.ScrollbarStyle
			}
			if rest != newLooks {
				t.Errorf("keys the file did not name moved: %+v", got)
			}
		})
	}
}

// TestV080DefaultsTypoFallsBackToTheNewDefault checks that a value outside an
// option's set lands on the new default, as the validator's warning says,
// rather than on the old one the renderer used to treat unknown values as.
func TestV080DefaultsTypoFallsBackToTheNewDefault(t *testing.T) {
	src := `[appearance]
dockbar_position = "botom"
window_title_position = "up"
click_to_type = "sometimes"

[appearance.scrollbar]
style = "thick"

[appearance.sidebar]
position = "middle"
`
	if got := loadLooks(t, &src); got != newLooks {
		t.Errorf("looks = %+v\nwant    %+v", got, newLooks)
	}
}

// TestV080FirstRunFileCarriesTheNewDefaults checks the file a fresh install
// writes names the new values, so it reads back as what is on screen.
func TestV080FirstRunFileCarriesTheNewDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(xdg.Reload)
	t.Setenv("XDG_CONFIG_HOME", dir)
	xdg.Reload()
	if _, err := config.LoadUserConfig(); err != nil {
		t.Fatalf("LoadUserConfig: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "tuios", "config.toml"))
	if err != nil {
		t.Fatalf("no first-run config written: %v", err)
	}
	body := string(data)
	for _, want := range []string{
		"dockbar_position = 'top'",
		"window_title_position = 'top'",
		"click_to_type = 'double'",
		"zoom_size = 95",
		"style = 'track'",
		"enabled = true",
		"position = 'right'",
		"width = 24",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("first-run config does not carry %q", want)
		}
	}

	// And it loads back to the same looks.
	if got := loadLooks(t, &body); got != newLooks {
		t.Errorf("first-run config loads as %+v\nwant %+v", got, newLooks)
	}
}
