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
//   - a string carries an escape sequence into the frame, or a key name or an
//     error carries one into the terminal that shows the plan;
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
		{name: "startup daemon", body: "[startup]\ndaemon = false", left: "startup.daemon"},
		{name: "startup default window", body: "[startup]\nopen_default_window = true", left: "startup.open_default_window"},
		{name: "dock session cell", body: "[dock.session]\nformat = \"{session}\"", left: "dock.session"},
		{name: "sidebar custom command", body: "[appearance.sidebar.custom]\ncommand = \"evil\"", left: "appearance.sidebar.custom"},
		{name: "behaviour option", body: "[appearance]\nfocus_follows_mouse = true", left: "appearance.focus_follows_mouse"},
		{name: "unknown appearance key", body: "[appearance]\nmade_up = 1", left: "appearance.made_up"},

		{name: "no version", body: "[appearance]\ngap = 1", err: "look = 1"},
		{name: "startup layout", body: "[startup]\nlayout = \"x\"", err: "startup.layout"},
		{name: "master ratio out of range", body: "[appearance]\nmaster_ratio = 99", err: "appearance.master_ratio"},
		{name: "master position", body: "[appearance]\nmaster_position = \"sideways\"", err: "appearance.master_position"},
		{name: "dock list not a list", body: "[dock]\nleft = \"mode\"", err: "dock.left must be a list"},
		{name: "dock list of numbers", body: "[dock]\nleft = [1, 2]", err: "dock.left must be a list"},
		{name: "dock list too long", body: "[dock]\nleft = [" + strings.Repeat("\"mode\",", 40) + "]", err: "at most"},
		{name: "escape in a dock item", body: "[dock]\nright = [\"\\u001b]52;c;eA==\\u0007\"]", err: "dock.right"},
		{name: "tab style", body: "[dock.tabs]\nstyle = \"spiky\"", err: "dock.tabs.style"},
		{name: "tab colour", body: "[dock.tabs]\nactive_fg = \"notacolour\"", err: "dock.tabs.active_fg"},
		{name: "tab bold not a bool", body: "[dock.tabs]\nactive_bold = \"yes\"", err: "dock.tabs.active_bold"},
		{name: "escape in a tab separator", body: "[dock.tabs]\nseparator = \"\\u001b[2J\"", err: "dock.tabs.separator"},
		{name: "long tab marker", body: "[dock.tabs]\nbell_marker = \"" + strings.Repeat("x", 64) + "\"", err: "dock.tabs.bell_marker"},
		{name: "escape in a workspace glyph", body: "[dock.tabs]\nnumber_glyphs = [\"I\", \"\\u001b[2J\"]", err: "number_glyphs"},
		{name: "too many workspace glyphs", body: "[dock.tabs]\nnumber_glyphs = [" + strings.Repeat("\"I\",", 10) + "]", err: "at most 9"},
		{name: "sections not text", body: "[appearance.sidebar]\nsections = [\"files\"]", err: "appearance.sidebar.sections must be text"},
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
		{name: "theme chrome group not a colour", body: "[theme]\nid = \"x\"\nbase = \"nord\"\n[theme.chrome.border]\nunfocused = 3", err: "border.unfocused"},
		{name: "glyph escape", body: "[glyphs]\nid = \"g\"\nclose = \"\\u001b[31mX\"", err: "control character"},
		{name: "glyph id with a path", body: "[glyphs]\nid = \"../g\"", err: "cannot be a file name"},
		{name: "theme not a table", body: "theme = \"nord\"", err: "must be a table"},
		{name: "bidi control in name", body: "name = \"abc\u202edef\"", err: "name"},
		{name: "escape in a theme the look names", body: "[appearance]\ntheme = \"\\u001b[2J\"\n[theme]\nid = \"x\"\nbase = \"nord\"", err: `"\x1b[2J"`},
		{name: "escape in a key name", body: "\"\\u001b]52;c;eA==\\u0007\" = 1", left: `"\x1b]52;c;eA==\a"`},
		{name: "escape in a nested key name", body: "[appearance]\n\"x\\u001b[2J\" = 1", left: `"appearance.x\x1b[2J"`},
		{name: "escape in a repeated key name", body: "\"\\u001b[2J\" = 1\n\"\\u001b[2J\" = 2", err: "already defined"},
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
				if strings.IndexFunc(err.Error(), theme.IsUnsafeRune) >= 0 {
					t.Fatalf("error %q carries a control character to the terminal", err)
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
	// A theme may nest chrome roles in groups, as theme files do.
	mustParseLook(t, "look = 1\n[theme]\nid = \"grouped\"\nbase = \"nord\"\n[theme.chrome.border]\nunfocused = \"#5c3b00\"\n")

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

// TestLookLeavesOwnFilesAlone holds apply and remove off the person's own
// files: a config.d/look.toml tuios did not write, and a theme or glyph set
// that already has the id a look would install.
func TestLookLeavesOwnFilesAlone(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(main, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	lk := mustParseLook(t, "look = 1\n[appearance]\ngap = 1\n[theme]\nid = \"own\"\nbase = \"nord\"\n")

	themes, err := theme.GetThemesDir()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(themes, 0o700); err != nil {
		t.Fatal(err)
	}
	ownTheme := filepath.Join(themes, "look-own.json")
	const themeBody = `{"id":"look-own","base":"dracula"}`
	if err := os.WriteFile(ownTheme, []byte(themeBody), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(ownTheme) })
	if _, err := PlanLook(main, lk, "test"); err == nil || !strings.Contains(err.Error(), "look-own") {
		t.Fatalf("PlanLook over a theme of the person's own: %v", err)
	}
	assertFile(t, ownTheme, themeBody)
	_ = os.Remove(ownTheme)

	own := LookPath(main)
	if err := os.MkdirAll(filepath.Dir(own), 0o700); err != nil {
		t.Fatal(err)
	}
	const ownBody = "# mine\n[appearance]\ngap = 7\n"
	if err := os.WriteFile(own, []byte(ownBody), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := PlanLook(main, lk, "test"); err == nil || !strings.Contains(err.Error(), "your own file") {
		t.Fatalf("PlanLook over a look.toml of the person's own: %v", err)
	}
	if _, err := PlanLookRemove(main); err == nil || !strings.Contains(err.Error(), "your own file") {
		t.Fatalf("PlanLookRemove of a look.toml of the person's own: %v", err)
	}
	assertFile(t, own, ownBody)
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

// TestParseLookItems holds the keys a look sets item by item. A dock list may
// name only built-in cells: "custom/NAME" draws a [dock.custom.NAME] cell,
// whose command runs, and the person's own config may define one that no list
// names yet. A rail layout may name every built-in section but custom, whose
// command appearance.sidebar.custom sets. Each such item is left out and
// named, and the rest of the list applies. The layout and dock tab keys a
// look may set come through with their values.
func TestParseLookItems(t *testing.T) {
	cases := []struct {
		name string
		body string
		want map[string]string // path to Text() of the setting; "" must be absent
		left []string
	}{
		{
			name: "custom cell in a dock list",
			body: "[dock]\nleft = [\"mode\", \"workspaces\"]\nright = [\"custom/weather\", \"clock\", \"session-controls\"]",
			want: map[string]string{
				"dock.left":  `["mode", "workspaces"]`,
				"dock.right": `["clock", "session-controls"]`,
			},
			left: []string{"dock.right: custom/weather"},
		},
		{
			name: "dock list of custom cells only",
			body: "[dock]\ncenter = [\"custom/np\"]",
			want: map[string]string{"dock.center": `[]`},
			left: []string{"dock.center: custom/np"},
		},
		{
			name: "unknown dock cell",
			body: "[dock]\nleft = [\"workspaces\", \"rm -rf\"]",
			want: map[string]string{"dock.left": `["workspaces"]`},
			left: []string{"dock.left: rm -rf"},
		},
		{
			name: "empty dock list",
			body: "[dock]\ncenter = []",
			want: map[string]string{"dock.center": `[]`},
		},
		{
			name: "custom table beside its list item",
			body: "[dock]\nright = [\"custom/x\"]\n[dock.custom.x]\ncommand = \"evil\"",
			want: map[string]string{"dock.right": `[]`},
			left: []string{"dock.custom", "dock.right: custom/x"},
		},
		{
			name: "custom rail section",
			body: "[appearance.sidebar]\nsections = \"sessions,terminals,files,custom\"",
			want: map[string]string{"appearance.sidebar.sections": "sessions,terminals,files"},
			left: []string{"appearance.sidebar.sections: custom"},
		},
		{
			name: "custom rail section with a share",
			body: "[appearance.sidebar]\nsections = \"sessions:40, CUSTOM:30 ,spacer,git\"",
			want: map[string]string{"appearance.sidebar.sections": "sessions:40,spacer,git"},
			left: []string{"appearance.sidebar.sections: CUSTOM:30"},
		},
		{
			name: "rail layout of the custom section only",
			body: "[appearance.sidebar]\nsections = \"custom,spacer\"",
			want: map[string]string{"appearance.sidebar.sections": ""},
			left: []string{"appearance.sidebar.sections", "appearance.sidebar.sections: custom"},
		},
		{
			name: "rail custom table",
			body: "[appearance.sidebar]\nsections = \"files\"\n[appearance.sidebar.custom]\ncommand = \"evil\"",
			want: map[string]string{"appearance.sidebar.sections": "files"},
			left: []string{"appearance.sidebar.custom"},
		},
		{
			name: "dock tabs",
			body: "[dock.tabs]\nstyle = \"flat\"\nseparator = \" \\u2022 \"\nseparator_fg = \"#4f5b58\"\nactive_fg = \"#272e33\"\n" +
				"active_bg = \"accent\"\nactive_bold = true\ninactive_fg = \"#859289\"\nnumber_glyphs = [\"I\", \"II\", \"\\u4e09\", \"\\U000f075a\"]\n" +
				"zoom_marker = \"Z\"\nshow_add = true\nattention_fg = \"red\"\nbell_marker = \"!\"\nactivity_marker = \"*\"",
			want: map[string]string{
				"dock.tabs.style":           "flat",
				"dock.tabs.separator":       " \u2022 ",
				"dock.tabs.separator_fg":    "#4f5b58",
				"dock.tabs.active_bg":       "accent",
				"dock.tabs.active_bold":     "true",
				"dock.tabs.number_glyphs":   `["I", "II", "三", "\U000f075a"]`,
				"dock.tabs.show_add":        "true",
				"dock.tabs.activity_marker": "*",
			},
		},
		{
			name: "layout",
			body: "[startup]\ntiled = true\nlayout = \"master-stack\"\n[appearance]\nmaster_position = \"center\"\nmaster_ratio = 46\n" +
				"scroll_column_width = 40\nzoom_size = 100\nzoom_max_width = 92",
			want: map[string]string{
				"startup.tiled":                  "true",
				"startup.layout":                 "master-stack",
				"appearance.master_position":     "center",
				"appearance.master_ratio":        "46",
				"appearance.scroll_column_width": "40",
				"appearance.zoom_size":           "100",
				"appearance.zoom_max_width":      "92",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lk, err := ParseLook([]byte("look = 1\n" + tc.body))
			if err != nil {
				t.Fatalf("ParseLook: %v", err)
			}
			got := map[string]string{}
			for _, s := range lk.Settings {
				got[s.Path] = s.Text()
			}
			for path, want := range tc.want {
				v, set := got[path]
				switch {
				case want == "" && set:
					t.Errorf("the look sets %s = %s", path, v)
				case want != "" && v != want:
					t.Errorf("%s = %q, want %q", path, v, want)
				}
			}
			left := slices.Clone(tc.left)
			slices.Sort(left)
			if !slices.Equal(lk.Left, left) {
				t.Errorf("Left = %q, want %q", lk.Left, left)
			}
			// What the look writes must load as the same config.
			frag, err := lk.fragment()
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := ParseUserConfig(frag)
			if err != nil {
				t.Fatalf("the fragment does not load: %v\n%s", err, frag)
			}
			for _, side := range []string{"left", "center", "right"} {
				for _, name := range cfg.Dock.DockList(side) {
					if !IsDockBuiltin(name) {
						t.Errorf("the written dock.%s names %q", side, name)
					}
				}
			}
			if strings.Contains(cfg.Appearance.Sidebar.Sections, SidebarSectionCustom) {
				t.Errorf("the written rail layout %q names the custom section", cfg.Appearance.Sidebar.Sections)
			}
		})
	}
}

// TestLookDockApply applies a look with dock lists and tabs over a config
// that defines a custom cell no list names. The plan names the dock changes,
// the config in force draws the look's built-ins, and the custom cell stays
// off the bar.
func TestLookDockApply(t *testing.T) {
	dir := t.TempDir()
	main := filepath.Join(dir, "config.toml")
	original := "[dock.custom.mine]\ncommand = \"date\"\n"
	if err := os.WriteFile(main, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	lk := mustParseLook(t, `look = 1
[dock]
left = ["workspaces"]
right = ["custom/mine", "clock"]
[dock.tabs]
style = "flat"
number_glyphs = ["I", "II"]
[appearance.sidebar]
sections = "files,custom"
`)
	plan, err := PlanLook(main, lk, "test")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ path, to string }{
		{"dock.left", `["workspaces"]`},
		{"dock.right", `["clock"]`},
		{"dock.tabs.style", "flat"},
		{"dock.tabs.number_glyphs", `["I", "II"]`},
		{"appearance.sidebar.sections", "files"},
	} {
		if !hasChange(plan, c.path, c.to) {
			t.Errorf("the plan does not change %s to %s: %v", c.path, c.to, plan.Changes)
		}
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	assertFile(t, main, original)
	cfg := loadEffective(t, main)
	if got := cfg.Dock.DockList("right"); !slices.Equal(got, []string{"clock"}) {
		t.Errorf("dock.right in force = %q", got)
	}
	if !cfg.Dock.Tabs.Flat() || cfg.Dock.Tabs.Icon(2) != "II" {
		t.Errorf("dock.tabs in force = %+v", cfg.Dock.Tabs)
	}
	if cfg.Appearance.Sidebar.Sections != "files" {
		t.Errorf("sections in force = %q", cfg.Appearance.Sidebar.Sections)
	}
	il, err := ReadInstalledLook(main)
	if err != nil || il == nil {
		t.Fatalf("ReadInstalledLook: %v", err)
	}
	for _, s := range il.Settings {
		if s.Path == "dock.right" && s.Text() != `["clock"]` {
			t.Errorf("look.toml holds dock.right = %s", s.Text())
		}
	}
}

// TestSetLookValue is the browser page's way to set a look key (see
// internal/learn). It takes what a look file may hold, and refuses a list
// with an item a look leaves out instead of dropping the item.
func TestSetLookValue(t *testing.T) {
	cfg := DefaultConfig()
	for _, tc := range []struct{ path, text string }{
		{"dock.left", `["mode","workspaces"]`},
		{"dock.center", `[]`},
		{"dock.tabs.style", "flat"},
		{"dock.tabs.active_bold", "true"},
		{"dock.tabs.number_glyphs", `["一","二"]`},
		{"appearance.sidebar.sections", "sessions,files"},
		{"appearance.master_ratio", "46"},
	} {
		if err := SetLookValue(cfg, tc.path, tc.text); err != nil {
			t.Errorf("SetLookValue(%s, %s): %v", tc.path, tc.text, err)
		}
	}
	if got := cfg.Dock.DockList("left"); !slices.Equal(got, []string{"mode", "workspaces"}) {
		t.Errorf("dock.left = %q", got)
	}
	if cfg.Dock.Center == nil || len(*cfg.Dock.Center) != 0 {
		t.Errorf("dock.center = %v, want an empty list", cfg.Dock.Center)
	}
	if !cfg.Dock.Tabs.Flat() || !cfg.Dock.Tabs.ActiveBold || cfg.Dock.Tabs.Icon(2) != "二" {
		t.Errorf("dock.tabs = %+v", cfg.Dock.Tabs)
	}
	before := *cfg
	for _, tc := range []struct{ path, text string }{
		{"dock.right", `["custom/x","clock"]`},
		{"dock.right", `["clock","made-up"]`},
		{"dock.right", `clock`},
		{"dock.custom.x.command", "evil"},
		{"appearance.sidebar.sections", "files,custom"},
		{"appearance.sidebar.sections", "custom:20"},
		{"dock.tabs.style", "spiky"},
		{"dock.tabs.separator", "\x1b[2J"},
		{"appearance.sidebar.editor", "evil"},
		{"startup.daemon", "false"},
	} {
		if err := SetLookValue(cfg, tc.path, tc.text); err == nil {
			t.Errorf("SetLookValue(%s, %q) was accepted", tc.path, tc.text)
		}
	}
	if got := cfg.Dock.DockList("right"); !slices.Equal(got, before.Dock.DockList("right")) {
		t.Errorf("a refused set changed dock.right to %q", got)
	}
	if cfg.Appearance.Sidebar.Sections != "sessions,files" {
		t.Errorf("a refused set changed the sections to %q", cfg.Appearance.Sidebar.Sections)
	}
}
