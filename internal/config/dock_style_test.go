package config

import (
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

// The style fields are embedded, so they have to be read from, and written
// back to, the table they sit in rather than a nested one.
func TestDockCellStyleTablesRoundTrip(t *testing.T) {
	src := `
[dock]
right = ["session", "custom/x"]

[dock.session]
format = "{session} {workspace}:{pane}"
fg = "#272e33"
bg = "#7fbbb3"
bold = true
cap_left = "<"
cap_right = ">"

[dock.custom.x]
command = "echo hi"
fg = "#ffffff"
`
	var cfg UserConfig
	if err := toml.Unmarshal([]byte(src), &cfg); err != nil {
		t.Fatal(err)
	}
	d := cfg.Dock
	want := DockCellStyle{Fg: "#272e33", Bg: "#7fbbb3", Bold: true, CapLeft: "<", CapRight: ">"}
	if d.Session.DockCellStyle != want || d.Session.Format != "{session} {workspace}:{pane}" {
		t.Errorf("[dock.session] read as %+v", d.Session)
	}
	if d.Custom["x"].Fg != "#ffffff" || d.Custom["x"].Command != "echo hi" {
		t.Errorf("[dock.custom.x] read as %+v", d.Custom["x"])
	}

	out, err := toml.Marshal(cfg.Dock)
	if err != nil {
		t.Fatal(err)
	}
	var back DockConfig
	if err := toml.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	if back.Session.DockCellStyle != want || back.Custom["x"].Fg != "#ffffff" {
		t.Errorf("round trip lost the style:\n%s", out)
	}
}

func TestValidateDockCellColors(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Dock.Session.Fg = "#12345"
	cfg.Dock.Custom = map[string]DockCustomConfig{
		"x": {Command: "echo", DockCellStyle: DockCellStyle{Bg: "lime"}},
		"y": {Command: "echo", DockCellStyle: DockCellStyle{Bg: "#7fbbb3", Fg: "bright_magenta"}},
		"z": {Command: "echo", DockCellStyle: DockCellStyle{Bg: "accent", Fg: "212"}},
	}
	result := &ValidationResult{}
	validateDock(cfg, result)
	var keys []string
	for _, w := range result.Warnings {
		keys = append(keys, w.Field+"."+w.Key)
	}
	got := strings.Join(keys, ",")
	for _, want := range []string{"dock.session.fg", "dock.custom.x.bg"} {
		if !strings.Contains(got, want) {
			t.Errorf("warnings %q do not name %s", got, want)
		}
	}
	for _, valid := range []string{"dock.custom.y.bg", "dock.custom.y.fg", "dock.custom.z.bg", "dock.custom.z.fg"} {
		if strings.Contains(got, valid) {
			t.Errorf("a valid colour was warned about: %s in %q", valid, got)
		}
	}
}
