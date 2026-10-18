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
	closeThemePicker(t, term, filterThemePicker(t, term, name), tuitest.Enter)
}

// filterThemePicker opens the theme picker and types name, so the picker
// shows the themes on the way. It returns name for closeThemePicker.
func filterThemePicker(t *testing.T, term *tuitest.Terminal, name string) string {
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
	return name
}

// closeThemePicker presses key (Enter to apply, Esc to cancel) and waits for
// the picker to close.
func closeThemePicker(t *testing.T, term *tuitest.Terminal, name string, key tuitest.Key) {
	t.Helper()
	if err := term.SendKeys(key); err != nil {
		t.Fatalf("close the theme picker on %q: %v", name, err)
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
// wins over the theme's ground. The focused pane in copy mode takes
// chrome.border.copy.
//
// How this could pass wrongly: the colours are ones no shipped theme or
// default draws, so a cell in one of them came from the theme file.
// Negative controls are in NEGATIVE_CONTROLS.md.
func TestThemeChromeRolesPaint(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[appearance]\ntheme = \"roles\"\n")
	writeConfigPart(t, base, "themes/roles.json", `{"id": "roles", "base": "nord", "chrome": {
		"border": {"unfocused": "#ff0000", "copy": "#ff00ff"}, "rule": "#0000ff", "dock": {"ground": "#00ff00"}}}`)
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

	// The focused pane in copy mode takes the theme's border.copy.
	enterTerminalMode(t, term)
	sendKeys(t, term, tuitest.Ctrl('b'), "[")
	copyBorder := func(c tuitest.Cell) bool {
		return is(c.Fg, 0xff, 0, 0xff) && strings.ContainsAny(c.Content, "─│╭╮╰╯┌┐└┘")
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return count(s, copyBorder) >= 10 }, uiTimeout); err != nil {
		t.Fatalf("the focused pane in copy mode never took the theme's border.copy: %v\n%s", err, term.SnapshotStyled())
	}
	saveArtifact(t, term, artifactDir(t), "copy-mode")
}

// TestBorderGradientSteps: appearance.border_focused_gradient draws the
// focused pane's top border from the first colour at its left end to the last
// at its right end (angle 0), in at most eight steps, on a floating pane and,
// after tiling is turned on, on the shared border grid of a tiled one.
//
// How this could pass wrongly: a border in one colour has one step, so the
// test requires three or more, red at the left end and blue at the right.
// Negative control in NEGATIVE_CONTROLS.md.
func TestBorderGradientSteps(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[appearance]\ntheme = \"nord\"\nborder_focused_gradient = \"#ff0000 #0000ff\"\nborder_gradient_angle = 0\n")
	term := startIn(t, base, startOpts{cols: 120, rows: 30, env: []string{"COLORTERM=truecolor"}})
	waitBoot(t, term)
	newWindow(t, term)

	// steps reads each run of border cells on one row, left to right, and
	// returns the colours along the run that changes colour most often.
	steps := func(s tuitest.Screen) []tuitest.Color {
		cols, rows := s.Size()
		var best []tuitest.Color
		for y := range rows {
			var run []tuitest.Color
			flush := func() {
				if len(run) > len(best) {
					best = run
				}
				run = nil
			}
			for x := range cols {
				c := s.Cell(x, y)
				if c.Content == "─" && c.Fg.Kind == tuitest.ColorRGB {
					if len(run) == 0 || run[len(run)-1] != c.Fg {
						run = append(run, c.Fg)
					}
					continue
				}
				flush()
			}
			flush()
		}
		return best
	}
	check := func(name string) {
		t.Helper()
		var got []tuitest.Color
		if err := term.WaitFor(func(s tuitest.Screen) bool {
			got = steps(s)
			return len(got) >= 3
		}, uiTimeout); err != nil {
			t.Fatalf("%s: the focused border never stepped through the gradient: %d steps\n%s", name, len(got), term.SnapshotStyled())
		}
		first, last := got[0], got[len(got)-1]
		if len(got) > 8 || first.R < first.B || last.B < last.R {
			t.Fatalf("%s: the border steps %+v, want at most 8 from red to blue\n%s", name, got, term.SnapshotStyled())
		}
		saveArtifact(t, term, artifactDir(t), name)
	}
	check("floating")

	sendKeys(t, term, "t")
	check("tiled")
}

// TestThemeEditorEditsAndSaves drives the theme editor the way a person does:
// the palette opens it on the theme in use, a value typed for the unfocused
// border shows at once on the pane without focus, ctrl+s writes a sparse
// file, and esc closes the editor with the saved theme in force.
//
// How this could pass wrongly: the colour is one nord does not draw, read off
// the frame, and the file is read back and must hold only the base and the
// one field.
func TestThemeEditorEditsAndSaves(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[appearance]\ntheme = \"nord\"\n")
	term := startIn(t, base, startOpts{cols: 140, rows: 40, env: []string{"COLORTERM=truecolor"}})
	waitBoot(t, term)
	newWindow(t, term)
	newWindow(t, term)

	sendKeys(t, term, legacyCtrlP)
	waitScreen(t, term, "the palette never opened", paletteTitle)
	sendKeys(t, term, "Edit theme")
	waitScreen(t, term, "the palette never filtered", "Edit theme")
	sendKeys(t, term, tuitest.Enter)
	waitScreen(t, term, "the theme editor never opened", "Edit theme: nord", "Unfocused border")
	saveArtifact(t, term, artifactDir(t), "opened")

	sendKeys(t, term, tuitest.Down, tuitest.Down, "e", "#ff0000", tuitest.Enter)
	red := func(c tuitest.Cell) bool {
		return c.Fg.Kind == tuitest.ColorRGB && c.Fg.R == 0xff && c.Fg.G == 0 && c.Fg.B == 0 &&
			strings.ContainsAny(c.Content, "─│╭╮╰╯")
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		n := 0
		cols, rows := s.Size()
		for y := range rows {
			for x := range cols {
				if red(s.Cell(x, y)) {
					n++
				}
			}
		}
		return n >= 10 && strings.Contains(s.Text(), "#ff0000")
	}, uiTimeout); err != nil {
		t.Fatalf("the typed border colour never showed on the pane without focus: %v\n%s", err, term.SnapshotStyled())
	}
	saveArtifact(t, term, artifactDir(t), "edited")

	sendKeys(t, term, tuitest.Ctrl('s'))
	file := filepath.Join(configDirIn(base), "themes", "nord.json")
	var got map[string]any
	deadline := time.Now().Add(uiTimeout)
	for {
		if data, err := os.ReadFile(file); err == nil && json.Unmarshal(data, &got) == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ctrl+s wrote no theme file at %s\n%s", file, term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
	want := map[string]any{"id": "nord", "base": "nord",
		"chrome": map[string]any{"border": map[string]any{"unfocused": "#ff0000"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the saved file holds %v, want only the base and the one field %v", got, want)
	}

	sendKeys(t, term, tuitest.Esc)
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !strings.Contains(s.Text(), "Edit theme: nord") }, uiTimeout); err != nil {
		t.Fatalf("esc did not close the theme editor: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "closed")
}

// TestThemeEditorQuickAndFix: ctrl+o in the theme picker starts a new theme
// on the highlighted one and opens the editor on the Quick tab. An accent too
// dark for its ground shows ! and the fix line, f lifts it in lightness only
// until it reads, and ctrl+s writes the new file with the base and the fixed
// accent.
//
// How this could pass wrongly: a fix that greys the colour also reads, so the
// fixed accent must still be red: its red channel above the other two.
func TestThemeEditorQuickAndFix(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[appearance]\ntheme = \"nord\"\n")
	term := startIn(t, base, startOpts{cols: 140, rows: 40, env: []string{"COLORTERM=truecolor"}})
	waitBoot(t, term)

	sendKeys(t, term, legacyCtrlP)
	waitScreen(t, term, "the palette never opened", paletteTitle)
	sendKeys(t, term, "Theme picker")
	waitScreen(t, term, "the palette never filtered", "Theme picker")
	sendKeys(t, term, tuitest.Enter)
	waitScreen(t, term, "the theme picker never opened", "themes")
	sendKeys(t, term, "nord")
	waitScreen(t, term, "the picker never filtered", "nord")
	sendKeys(t, term, tuitest.Ctrl('o'))
	waitScreen(t, term, "ctrl+o never opened the editor on a new theme", "Edit theme: nord-custom", "Seeds", "Dark theme")

	sendKeys(t, term, "e", "#330000", tuitest.Enter)
	waitScreen(t, term, "a dark accent never asked for a fix", "#330000", "Press f to fix contrast.")
	saveArtifact(t, term, artifactDir(t), "too-dark")

	sendKeys(t, term, "f")
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		text := s.Text()
		return !strings.Contains(text, "#330000") && !strings.Contains(text, "Press f to fix contrast.")
	}, uiTimeout); err != nil {
		t.Fatalf("f did not fix the accent's contrast: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "fixed")

	sendKeys(t, term, tuitest.Ctrl('s'))
	file := filepath.Join(configDirIn(base), "themes", "nord-custom.json")
	var got struct {
		ID     string `json:"id"`
		Base   string `json:"base"`
		Chrome struct {
			Accent string `json:"accent"`
		} `json:"chrome"`
	}
	deadline := time.Now().Add(uiTimeout)
	for {
		if data, err := os.ReadFile(file); err == nil && json.Unmarshal(data, &got) == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ctrl+s wrote no theme file at %s\n%s", file, term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if got.ID != "nord-custom" || got.Base != "nord" {
		t.Fatalf("the new theme saved as id %q base %q, want nord-custom on nord", got.ID, got.Base)
	}
	var r, g, b uint8
	if _, err := fmt.Sscanf(got.Chrome.Accent, "#%02x%02x%02x", &r, &g, &b); err != nil {
		t.Fatalf("the saved accent %q is not hex: %v", got.Chrome.Accent, err)
	}
	if r <= g || r <= b || r <= 0x33 {
		t.Fatalf("the fixed accent %s is not a lighter red", got.Chrome.Accent)
	}
}

// TestAfterThemeChangeHookFiresOncePerPick runs a hook on after-theme-change
// that appends the theme it was given to a file.
//
// The ways it could go wrong, written down before the code:
//   - it fires at startup, which is not a change;
//   - it fires for each theme the picker shows while the name is typed;
//   - it fires for a pick that was cancelled, which puts the old theme back;
//   - it gives the wrong theme, background or light flag;
//   - it fires before the picker closes and misses the theme picked.
//
// So the log must stay empty after startup, hold one line for Dracula, stay
// at one line after a cancelled Latte pick, and hold a second line for Latte.
// The file is kept as the artifact.
func TestAfterThemeChangeHookFiresOncePerPick(t *testing.T) {
	base := t.TempDir()
	out := filepath.Join(base, "theme-hook.log")
	writeConfig(t, base, "[appearance]\ntheme = \"tokyonight\"\n\n[hooks]\n"+
		"after-theme-change = 'echo \"$TUIOS_EVENT $TUIOS_THEME $TUIOS_THEME_BG $TUIOS_THEME_LIGHT\" >> "+out+"'\n")
	term := startIn(t, base, startOpts{cols: 120, rows: 40})
	waitBoot(t, term)
	artifacts := artifactDir(t)

	// waitLines waits until the log has n lines and returns them.
	waitLines := func(n int) []string {
		t.Helper()
		var got []string
		deadline := time.Now().Add(shellTimeout)
		for time.Now().Before(deadline) {
			data, _ := os.ReadFile(out)
			got = strings.Split(strings.TrimSpace(string(data)), "\n")
			if len(data) == 0 {
				got = nil
			}
			if len(got) >= n {
				return got
			}
			time.Sleep(100 * time.Millisecond)
		}
		return got
	}
	// Startup is not a change. Hooks run in their own goroutine, so give a
	// firing time to land before saying there was none.
	time.Sleep(time.Second)
	if got := waitLines(0); len(got) != 0 {
		t.Fatalf("the hook fired at startup:\n%s", strings.Join(got, "\n"))
	}

	pickTheme(t, term, "dracula")
	got := waitLines(1)
	if len(got) != 1 {
		t.Fatalf("after one pick the hook ran %d times, want 1:\n%s", len(got), strings.Join(got, "\n"))
	}
	f := strings.Fields(got[0])
	if len(f) != 4 || f[0] != "after-theme-change" || f[1] != "dracula" || len(f[2]) != 7 || f[2][0] != '#' || f[3] != "0" {
		t.Errorf("the hook was given %q, want \"after-theme-change dracula #rrggbb 0\"", got[0])
	}

	// A cancelled pick puts Dracula back, which is no change.
	closeThemePicker(t, term, filterThemePicker(t, term, "catppuccin_latte"), tuitest.Esc)
	time.Sleep(time.Second)

	pickTheme(t, term, "catppuccin_latte")
	got = waitLines(2)
	time.Sleep(500 * time.Millisecond)
	got = waitLines(len(got))
	if len(got) != 2 {
		t.Fatalf("after a cancelled pick and a second pick the hook ran %d times, want 2:\n%s", len(got), strings.Join(got, "\n"))
	}
	f = strings.Fields(got[1])
	if len(f) != 4 || f[1] != "catppuccin_latte" || f[2] == strings.Fields(got[0])[2] || f[3] != "1" {
		t.Errorf("the hook was given %q after the Latte pick, want catppuccin_latte, a new background and 1", got[1])
	}
	data, _ := os.ReadFile(out)
	if err := os.WriteFile(filepath.Join(artifacts, "theme-hook.log"), data, 0o644); err != nil {
		t.Errorf("keep the hook log: %v", err)
	}
	saveArtifact(t, term, artifacts, "after-latte")
}
