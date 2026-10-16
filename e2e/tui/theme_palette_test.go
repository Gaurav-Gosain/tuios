package tuie2e

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// probeCmd prints an SGR 31 marker the shell assembles itself, so the command
// line the harness typed never carries the marker and every hit is real output.
func probeCmd(tag string) string {
	return `printf '\033[31m%s` + tag + `\033[0m\n' "$(echo INK)"`
}

// probeInk returns the colour the host terminal was given for the first cell of
// marker, which is the only place the question can honestly be asked: what
// tuios put on the wire, read off the terminal it wrote to.
func probeInk(t *testing.T, term *tuitest.Terminal, marker string) tuitest.Color {
	t.Helper()
	if err := term.WaitForText(marker, uiTimeout); err != nil {
		t.Fatalf("the probe never printed %q: %v\n%s", marker, err, term.Snapshot())
	}
	s := term.Screen()
	_, rows := s.Size()
	for row := rows - 1; row >= 0; row-- {
		if col := strings.Index(s.Line(row), marker); col >= 0 {
			return s.Cell(col, row).Fg
		}
	}
	t.Fatalf("no row carried %q on its own\n%s", marker, term.Snapshot())
	return tuitest.Color{}
}

// pickTheme drives the theme picker to a name and waits for it to close.
func pickTheme(t *testing.T, term *tuitest.Terminal, name string) {
	t.Helper()
	if err := term.SendKeys(tuitest.Ctrl('p')); err != nil {
		t.Fatalf("open palette: %v", err)
	}
	if err := term.WaitForText(paletteTitle, uiTimeout); err != nil {
		t.Fatalf("palette did not open: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("Theme picker"); err != nil {
		t.Fatalf("type palette query: %v", err)
	}
	if err := term.WaitForText("Theme picker", uiTimeout); err != nil {
		t.Fatalf("palette never filtered to the theme picker: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("open the theme picker: %v", err)
	}
	if err := term.WaitForText("themes", uiTimeout); err != nil {
		t.Fatalf("the theme picker did not open: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(name); err != nil {
		t.Fatalf("type the theme name: %v", err)
	}
	if err := term.WaitForText(name, uiTimeout); err != nil {
		t.Fatalf("the picker never filtered to %q: %v\n%s", name, err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatalf("apply the theme: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "themes")
	}, uiTimeout); err != nil {
		t.Fatalf("the theme picker did not close: %v\n%s", err, term.Snapshot())
	}
	time.Sleep(insertGuard)
}

// TestThemeOffLeavesThePaletteToTheTerminal is the on-screen form of the rule.
// A palette index belongs to the user's terminal, so with no theme loaded an
// SGR 31 in a pane has to reach the host as an index and be painted from the
// user's own red. Applying a theme is what replaces it, and removing that theme
// has to give it back.
//
// Before the palette was split into a theme layer and the guest's own, turning
// a theme off left all sixteen of its colours in the emulator: every pane went
// on painting in a theme the user had just removed, for the life of the
// session.
func TestThemeOffLeavesThePaletteToTheTerminal(t *testing.T) {
	term, _ := start(t, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)

	runInShell(t, term, probeCmd("A"), "INKA", shellTimeout)
	if got := probeInk(t, term, "INKA"); got.Kind != tuitest.ColorIndexed || got.Index != 1 {
		t.Errorf("with no theme, SGR 31 reached the host as %+v, want palette index 1", got)
	}

	leaveTerminalMode(t, term)
	pickTheme(t, term, "catppuccin_mocha")
	enterTerminalMode(t, term)

	// A theme replaces the index with its own colour. The harness terminal is
	// not truecolor, so that colour arrives stepped down to a 256 slot; what
	// makes the point either way is that slot 1, the user's own red, is no
	// longer what the host was asked for.
	runInShell(t, term, probeCmd("B"), "INKB", shellTimeout)
	if got := probeInk(t, term, "INKB"); got.Kind == tuitest.ColorIndexed && got.Index == 1 {
		t.Errorf("with a theme, SGR 31 still reached the host as %+v, want the theme's own colour", got)
	}

	leaveTerminalMode(t, term)
	pickTheme(t, term, "none")
	enterTerminalMode(t, term)

	runInShell(t, term, probeCmd("C"), "INKC", shellTimeout)
	if got := probeInk(t, term, "INKC"); got.Kind != tuitest.ColorIndexed || got.Index != 1 {
		t.Errorf("after the theme was removed, SGR 31 reached the host as %+v, want palette index 1 back", got)
	}
}

// TestPickerShowsTheUsersOwnSixteen is the picker half of the same rule. With
// no theme loaded the row of sixteen at the top of the accent picker is the
// user's terminal palette, so every swatch has to be painted with an index and
// left for the host to resolve. Painted from a table of xterm defaults instead,
// as it was, the row shows sixteen colours the user may not have.
func TestPickerShowsTheUsersOwnSixteen(t *testing.T) {
	term, _ := start(t, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	newWindow(t, term)

	toggleSidebarViaPalette(t, term)
	if err := term.WaitForText(sidebarHeader, uiTimeout); err != nil {
		t.Fatalf("sidebar did not open: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("s"); err != nil {
		t.Fatalf("focus the rail: %v", err)
	}
	if err := term.SendKeys("l"); err != nil {
		t.Fatalf("move the rail cursor: %v", err)
	}
	time.Sleep(insertGuard)
	if err := term.SendKeys("c"); err != nil {
		t.Fatalf("open the accent picker: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return strings.Contains(text, "accent") && strings.Contains(text, "hex")
	}, uiTimeout); err != nil {
		t.Fatalf("the accent picker did not open: %v\n%s", err, term.Snapshot())
	}

	// The sixteen are the only cells in the dialog whose backgrounds are palette
	// indices, which is the whole claim: found by that property rather than by
	// counting rows, so the layout can move without the test lying.
	s := term.Screen()
	cols, rows := s.Size()
	seen := map[uint8]bool{}
	for row := range rows {
		for col := range cols {
			if bg := s.Cell(col, row).Bg; bg.Kind == tuitest.ColorIndexed && bg.Index < 16 {
				seen[bg.Index] = true
			}
		}
	}
	// Bright black has no normal twin in the picker's grid, so fifteen of the
	// sixteen slots are on offer.
	if len(seen) < 15 {
		t.Errorf("the picker painted %d of the terminal's own palette slots, want 15\n%s", len(seen), term.Snapshot())
	}
}

// TestAnEditedThemeFileRepaints is the save-and-look loop: the theme in use is
// a file in the themes directory, and saving that file puts the edited colours
// in force with no restart and no reselect. The file is saved the way an
// editor saves, a new file renamed into place.
//
// Negative control: a build without the themes watch keeps the old red.
func TestAnEditedThemeFileRepaints(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[appearance]\ntheme = \"mine\"\n")
	writeConfigPart(t, base, "themes/mine.json", `{"id": "mine", "base": "nord", "red": "#ff0000"}`)
	term := startIn(t, base, startOpts{cols: 120, rows: 40, env: []string{"COLORTERM=truecolor"}})
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)

	runInShell(t, term, probeCmd("A"), "INKA", shellTimeout)
	if got := probeInk(t, term, "INKA"); got.Kind != tuitest.ColorRGB || got.R != 0xff || got.G != 0 {
		t.Fatalf("the theme's red reached the host as %+v, want #ff0000", got)
	}

	writeConfigPart(t, base, "themes/mine.json", `{"id": "mine", "base": "nord", "red": "#00ff00"}`)
	// The reload lands after the watcher's debounce, so the probe is printed
	// again until it shows, within the usual wait.
	deadline := time.Now().Add(uiTimeout)
	for i := 0; ; i++ {
		marker := fmt.Sprintf("INKB%d", i)
		runInShell(t, term, probeCmd(fmt.Sprintf("B%d", i)), marker, shellTimeout)
		got := probeInk(t, term, marker)
		if got.Kind == tuitest.ColorRGB && got.R == 0 && got.G == 0xff {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("after the theme file was saved, the theme's red reached the host as %+v, want #00ff00\n%s",
				got, term.Snapshot())
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// TestThemeExportWritesOnlyTheChanges: tuios theme export writes a theme file
// against the built-in it starts from, through a base that is another file,
// with only the colours that differ. The file loads as the same theme under a
// new id, and tuios theme share carries the same file.
//
// How this could pass wrongly: an export of every colour also loads as the
// same theme, so the test first requires the sparse shape. Negative controls
// are in NEGATIVE_CONTROLS.md.
func TestThemeExportWritesOnlyTheChanges(t *testing.T) {
	base := t.TempDir()
	writeConfigPart(t, base, "themes/ember.json",
		`{"id": "ember", "base": "dracula", "red": "#ff5500", "chrome": {"accent": "bright_red", "surface": "#1e1e2e"}}`)
	writeConfigPart(t, base, "themes/ember2.json", `{"id": "ember2", "base": "ember", "green": "#00ff00"}`)
	dir := artifactDir(t)
	run := func(name string, args ...string) string {
		t.Helper()
		out, err := tuiosCLI(t, base, args...)
		if err != nil {
			t.Fatalf("tuios %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(out), 0o644); err != nil {
			t.Fatalf("save %s: %v", name, err)
		}
		return out
	}
	decode := func(what, body string) map[string]any {
		t.Helper()
		var m map[string]any
		if err := json.Unmarshal([]byte(body), &m); err != nil {
			t.Fatalf("%s is not JSON: %v\n%s", what, err, body)
		}
		return m
	}

	sparse := run("ember2.json", "theme", "export", "ember2")
	got := decode("the export", sparse)
	want := map[string]any{
		"id": "ember2", "base": "dracula", "red": "#ff5500", "green": "#00ff00",
		"chrome": map[string]any{"accent": "bright_red", "surface": "#1e1e2e"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the export of ember2 is\n%s\nwant only the base and the changes: %v", sparse, want)
	}

	// The same file under another id is the same theme.
	writeConfigPart(t, base, "themes/copy.json", strings.Replace(sparse, `"ember2"`, `"copy"`, 1))
	full := decode("the full export", run("ember2-full.json", "theme", "export", "ember2", "--full"))
	copied := decode("the full export of the copy", run("copy-full.json", "theme", "export", "copy", "--full"))
	copied["id"] = "ember2"
	if !reflect.DeepEqual(full, copied) {
		t.Fatalf("the exported file loads as another theme:\n%v\nwant\n%v", copied, full)
	}

	line := strings.TrimSpace(run("ember2.share", "theme", "share", "ember2"))
	body, ok := strings.CutPrefix(line, "tuios-theme:v1:")
	if !ok {
		t.Fatalf("the share string %q does not start with tuios-theme:v1:", line)
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		t.Fatalf("the share string is not base64url: %v", err)
	}
	if shared := decode("the share string", string(raw)); !reflect.DeepEqual(shared, want) {
		t.Fatalf("the share string holds\n%s\nwant the export", raw)
	}

	scheme := run("ember.base16.yaml", "theme", "export", "ember", "--format", "base16")
	for _, row := range []string{`base00: "#1e1f28"`, `base01: "#1e1e2e"`, `base08: "#ff5500"`, `base0F: "#`} {
		if !strings.Contains(scheme, row) {
			t.Fatalf("the base16 scheme has no %s\n%s", row, scheme)
		}
	}
}

// TestThemeChromeRolesPaint: a theme's chrome.border.unfocused colours the
// border of a pane without focus, chrome.rule colours the dock's rule, and
// chrome.dock.ground paints the dock's row. A dock_background in the config
// wins over the theme's ground.
//
// How this could pass wrongly: the colours are ones no shipped theme or
// default draws, so a cell in one of them came from the theme file.
// Negative controls are in NEGATIVE_CONTROLS.md.
func TestThemeChromeRolesPaint(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[appearance]\ntheme = \"roles\"\n")
	writeConfigPart(t, base, "themes/roles.json", `{"id": "roles", "base": "nord", "chrome": {
		"border": {"unfocused": "#ff0000"}, "rule": "#0000ff", "dock": {"ground": "#00ff00"}}}`)
	term := startIn(t, base, startOpts{cols: 120, rows: 30, env: []string{"COLORTERM=truecolor"}})
	waitBoot(t, term)
	newWindow(t, term)
	newWindow(t, term)

	is := func(c tuitest.Color, r, g, b uint8) bool {
		return c.Kind == tuitest.ColorRGB && c.R == r && c.G == g && c.B == b
	}
	count := func(s tuitest.Screen, match func(tuitest.Cell) bool) int {
		n := 0
		cols, rows := s.Size()
		for y := range rows {
			for x := range cols {
				if match(s.Cell(x, y)) {
					n++
				}
			}
		}
		return n
	}
	unfocused := func(c tuitest.Cell) bool {
		return is(c.Fg, 0xff, 0, 0) && strings.ContainsAny(c.Content, "─│╭╮╰╯┌┐└┘")
	}
	rule := func(c tuitest.Cell) bool { return is(c.Fg, 0, 0, 0xff) && c.Content == "─" }
	dockRowGround := func(s tuitest.Screen) bool {
		r := dockStatusRow(s)
		return r >= 0 && is(s.Cell(cellColumnOf(s, r, "1:"), r).Bg, 0, 0xff, 0)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return count(s, unfocused) >= 10 && count(s, rule) >= 10 && dockRowGround(s)
	}, uiTimeout); err != nil {
		t.Fatalf("the theme's border.unfocused, rule and dock.ground never painted: %v\n%s", err, term.SnapshotStyled())
	}
	saveArtifact(t, term, artifactDir(t), "roles")

	writeConfig(t, base, "[appearance]\ntheme = \"roles\"\ndock_background = \"off\"\n")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		r := dockStatusRow(s)
		return r >= 0 && !dockRowGround(s)
	}, uiTimeout); err != nil {
		t.Fatalf("dock_background = \"off\" in the config did not win over the theme's dock.ground: %v\n%s", err, term.SnapshotStyled())
	}
	saveArtifact(t, term, artifactDir(t), "config-wins")
}
