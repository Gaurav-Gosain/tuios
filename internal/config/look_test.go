package config

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// A look is written by a stranger and applied by a person who trusts the
// gallery card, so ParseLook is a security boundary. The ways it can fail:
//
//   - a key that runs a program, names a file, reaches the network or the
//     daemon gets through, at the top level, nested in an allowed section,
//     or as an array of tables;
//   - a value gets through that the option's own check refuses;
//   - a string carries an escape sequence into the frame;
//   - a theme or glyph set id is a path and writes outside its directory;
//   - an oversized file or a future version is read anyway.
//
// Each row below is one of those. A row with left names the key the look must
// leave out; a row with err must fail the whole look.
func TestParseLookBoundary(t *testing.T) {
	cases := []struct {
		name string
		body string
		left string // a key that must be in Left and not in Settings
		err  string // a substring of the error, when the look must fail
	}{
		{name: "hooks table", body: "[hooks]\nafter-agent-state = \"curl x | sh\"", left: "hooks"},
		{name: "keybinding command", body: "[[keybindings.command]]\nkey = \"x\"\nrun = \"rm -rf ~\"", left: "keybindings"},
		{name: "include", body: "include = [\"/etc/passwd\"]", left: "include"},
		{name: "preferred shell", body: "[appearance]\npreferred_shell = \"/bin/evil\"", left: "appearance.preferred_shell"},
		{name: "link opener", body: "[appearance]\nlink_opener = \"evil\"", left: "appearance.link_opener"},
		{name: "copy command", body: "[appearance.selection]\ncopy_command = \"evil\"", left: "appearance.selection.copy_command"},
		{name: "osc52 write", body: "[appearance.selection]\nosc52_write = \"always\"", left: "appearance.selection.osc52_write"},
		{name: "sidebar editor", body: "[appearance.sidebar]\neditor = \"evil\"", left: "appearance.sidebar.editor"},
		{name: "file delete", body: "[appearance.sidebar]\nfile_delete = \"rm\"", left: "appearance.sidebar.file_delete"},
		{name: "custom sidebar section", body: "[appearance.sidebar.custom.x]\ncommand = \"evil\"", left: "appearance.sidebar.custom"},
		{name: "dock custom script", body: "[dock.custom.x]\ncommand = \"evil\"", left: "dock.custom"},
		{name: "notification command", body: "[notifications.agent]\ncommand = \"evil\"", left: "notifications"},
		{name: "tape autorun", body: "[tape]\nautorun = \"x.tape\"", left: "tape"},
		{name: "launcher", body: "[launcher]\ngui_command = \"evil\"", left: "launcher"},
		{name: "screenshot directory", body: "[screenshot]\ndirectory = \"/etc\"", left: "screenshot"},
		{name: "host files roots", body: "[hosts.x]\naddress = \"evil\"\nfiles_roots = [\"/\"]", left: "hosts"},
		{name: "daemon", body: "[daemon]\nlog_level = \"debug\"", left: "daemon"},
		{name: "permissions", body: "[permissions]\nx = 1", left: "permissions"},
		{name: "plugins", body: "[plugins]\nx = 1", left: "plugins"},
		{name: "startup layout", body: "[startup]\nlayout = \"x\"", left: "startup"},
		{name: "behaviour option", body: "[appearance]\nfocus_follows_mouse = true", left: "appearance.focus_follows_mouse"},
		{name: "unknown appearance key", body: "[appearance]\nmade_up = 1", left: "appearance.made_up"},

		{name: "no version", body: "[appearance]\ngap = 1", err: "look = 1"},
		{name: "future version", body: "look = 2", err: "Update tuios"},
		{name: "bad version type", body: "look = \"1\"", err: "whole number"},
		{name: "bad int", body: "[appearance]\ngap = \"lots\"", err: "appearance.gap"},
		{name: "int out of range", body: "[appearance]\ndim_unfocused = 1000", err: "appearance.dim_unfocused"},
		{name: "float", body: "[appearance]\ngap = 1.5", err: "appearance.gap"},
		{name: "enum", body: "[appearance]\nborder_style = \"spiky\"", err: "appearance.border_style"},
		{name: "colour", body: "[appearance]\nborder_focused_color = \"notacolour\"", err: "border_focused_color"},
		{name: "escape in title format", body: "[appearance]\nwindow_title_format = \"\\u001b]52;c;eA==\\u0007{title}\"", err: "window_title_format"},
		{name: "escape in name", body: "name = \"a\\u001bb\"", err: "name"},
		{name: "unknown theme", body: "[appearance]\ntheme = \"../../etc/passwd\"", err: "appearance.theme"},
		{name: "theme id with a path", body: "[theme]\nid = \"../evil\"", err: "cannot be a file name"},
		{name: "theme id with a slash", body: "[theme]\nid = \"a/b\"", err: "cannot be a file name"},
		{name: "theme id hidden file", body: "[theme]\nid = \".x\"", err: "cannot be a file name"},
		{name: "theme without id", body: "[theme]\nbase = \"nord\"", err: "needs an id"},
		{name: "theme unknown base", body: "[theme]\nid = \"x\"\nbase = \"nope\"", err: "base"},
		{name: "theme and another theme", body: "[appearance]\ntheme = \"nord\"\n[theme]\nid = \"x\"\nbase = \"nord\"", err: "appearance.theme"},
		{name: "glyph escape", body: "[glyphs]\nid = \"g\"\nclose = \"\\u001b[31mX\"", err: "control character"},
		{name: "glyph id with a path", body: "[glyphs]\nid = \"../g\"", err: "cannot be a file name"},
		{name: "theme not a table", body: "theme = \"nord\"", err: "must be a table"},
		{name: "oversized", body: "name = \"" + strings.Repeat("a", MaxLookBytes) + "\"", err: "larger than"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := tc.body
			if !strings.HasPrefix(body, "look") && tc.name != "no version" {
				body = "look = 1\n" + body
			}
			lk, err := ParseLook([]byte(body))
			if tc.err != "" {
				if err == nil {
					t.Fatalf("the look was accepted: settings %v, left %v", lk.Settings, lk.Left)
				}
				if !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("error %q does not mention %q", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseLook: %v", err)
			}
			if !slices.Contains(lk.Left, tc.left) {
				t.Errorf("Left = %v, want it to name %s", lk.Left, tc.left)
			}
			for _, s := range lk.Settings {
				if s.Path == tc.left || strings.HasPrefix(s.Path, tc.left+".") {
					t.Errorf("the look sets %s", s.Path)
				}
			}
		})
	}
}

// TestLookKeysAreLookKeys holds the allowlist to what it claims: every key is
// a registered option, none is deprecated, none reaches the host, and no key
// whose name says it runs or names something is on it.
func TestLookKeysAreLookKeys(t *testing.T) {
	if len(LookKeys()) != len(lookKeys) {
		for _, k := range lookKeys {
			if _, ok := LookupOption(k); !ok {
				t.Errorf("look key %s is not a registered option", k)
			}
		}
	}
	for _, k := range LookKeys() {
		opt, _ := LookupOption(k)
		if opt.Deprecated != "" {
			t.Errorf("look key %s is deprecated", k)
		}
		if ReachesHost(k) {
			t.Errorf("look key %s reaches the host", k)
		}
		last := k[strings.LastIndex(k, ".")+1:]
		for _, word := range []string{"command", "shell", "opener", "editor", "file", "dir", "path", "script", "host", "url", "exec", "osc52", "delete"} {
			if strings.Contains(last, word) && last != "file_icons" && last != "file_icon_colors" {
				t.Errorf("look key %s has %q in its name", k, word)
			}
		}
	}
}

// TestLookKeysJSONCurrent keeps the list the docs gallery vendors in step with
// the build. Regenerate it with: go run ./cmd/tuios look keys --json > docs/look-keys.json
func TestLookKeysJSONCurrent(t *testing.T) {
	want, err := LookKeysJSON()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join("..", "..", "docs", "look-keys.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("docs/look-keys.json is out of date. Run: go run ./cmd/tuios look keys --json > docs/look-keys.json")
	}
}

// TestLookApplyRemoveRoundTrip applies a look with a theme over a config,
// changes a look key the way the settings page does, applies a second look,
// and removes it. config.toml must only ever gain the settings change, and the
// remove must leave no look file or installed asset behind.
func TestLookApplyRemoveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "config.toml")
	original := "# my config\n[appearance]\nwindow_title_position = \"top\" # keep\n"
	if err := os.WriteFile(main, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}

	first := mustParseLook(t, `look = 1
name = "First"
[appearance]
gap = 2
border_style = "double"
window_title_position = "bottom"
[appearance.sidebar]
row_style = "pill"
[theme]
id = "first"
base = "nord"
bg = "#101020"
[hooks]
x = "evil"
`)
	plan, err := PlanLook(main, first, "test")
	if err != nil {
		t.Fatal(err)
	}
	wantShadow := []LookShadow{{Path: "appearance.window_title_position", File: "config.toml"}}
	if !slices.Equal(plan.Shadowed, wantShadow) {
		t.Errorf("Shadowed = %v, want %v", plan.Shadowed, wantShadow)
	}
	if !hasChange(plan, "appearance.gap", "2") || !hasChange(plan, "appearance.theme", "look-first") {
		t.Errorf("Changes = %v", plan.Changes)
	}
	if hasChange(plan, "appearance.window_title_position", "bottom") {
		t.Error("the plan changes a key config.toml hides")
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	assertFile(t, main, original)
	if !theme.Exists("look-first") {
		t.Fatal("the theme look-first is not installed")
	}
	cfg := loadEffective(t, main)
	if cfg.Appearance.Gap != 2 || cfg.Appearance.Theme != "look-first" || cfg.Appearance.WindowTitlePosition != "top" {
		t.Fatalf("config in force: gap %d, theme %q, title %q", cfg.Appearance.Gap, cfg.Appearance.Theme, cfg.Appearance.WindowTitlePosition)
	}

	// A settings change of a key the look sets goes to config.toml.
	base, _ := MarshalUserConfig(cfg)
	if err := SetOptionValue(cfg, "appearance.gap", "3"); err != nil {
		t.Fatal(err)
	}
	next, _ := MarshalUserConfig(cfg)
	if _, err := saveConfigData(main, base, next, saveSeq.Add(1)); err != nil {
		t.Fatal(err)
	}
	lookData, _ := os.ReadFile(LookPath(main))
	if !strings.Contains(string(lookData), "gap = 2") {
		t.Errorf("the save changed look.toml:\n%s", lookData)
	}
	afterSave, _ := os.ReadFile(main)
	if !strings.Contains(string(afterSave), "gap = 3") || !strings.HasPrefix(string(afterSave), original) {
		t.Errorf("config.toml after the save:\n%s", afterSave)
	}

	// A second look replaces the first and its theme.
	second := mustParseLook(t, "look = 1\nname = \"Second\"\n[appearance]\nborder_style = \"rounded\"\n[theme]\nid = \"second\"\nbase = \"nord\"\n")
	plan, err = PlanLook(main, second, "test")
	if err != nil {
		t.Fatal(err)
	}
	if plan.Current == nil || plan.Current.Theme != "look-first" {
		t.Fatalf("Current = %+v, want the first look", plan.Current)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	if themeFileThere(t, "look-first") || !themeFileThere(t, "look-second") {
		t.Error("the second apply did not swap the theme files")
	}
	if cfg := loadEffective(t, main); cfg.Appearance.Gap != 3 || cfg.Appearance.Sidebar.RowStyle == "pill" {
		t.Errorf("after the second look: gap %d, row style %q", cfg.Appearance.Gap, cfg.Appearance.Sidebar.RowStyle)
	}

	plan, err = PlanLookRemove(main)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(LookPath(main)); !os.IsNotExist(err) {
		t.Error("look.toml is still there")
	}
	if themeFileThere(t, "look-second") {
		t.Error("the theme look-second is still there")
	}
	assertFile(t, main, string(afterSave))
	if il, _ := ReadInstalledLook(main); il != nil {
		t.Errorf("a look is still installed: %+v", il)
	}
}

// TestLookReadOnlyConfig refuses a config folder tuios cannot write.
func TestLookReadOnlyConfig(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes any folder")
	}
	dir := t.TempDir()
	main := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(main, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	_, err := PlanLook(main, mustParseLook(t, "look = 1\n[appearance]\ngap = 1\n"), "test")
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("PlanLook on a read-only config: %v", err)
	}
}

func mustParseLook(t *testing.T, body string) *Look {
	t.Helper()
	lk, err := ParseLook([]byte(body))
	if err != nil {
		t.Fatalf("ParseLook: %v", err)
	}
	return lk
}

func hasChange(p *LookPlan, path, to string) bool {
	for _, c := range p.Changes {
		if c.Path == path && c.To == to {
			return true
		}
	}
	return false
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s changed:\n%s\nwant:\n%s", filepath.Base(path), got, want)
	}
}

func loadEffective(t *testing.T, main string) *UserConfig {
	t.Helper()
	cfg, _, err := loadConfigFile(main)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func themeFileThere(t *testing.T, id string) bool {
	t.Helper()
	dir, err := theme.GetThemesDir()
	if err != nil {
		t.Fatal(err)
	}
	_, err = os.Stat(filepath.Join(dir, id+".json"))
	return err == nil
}
