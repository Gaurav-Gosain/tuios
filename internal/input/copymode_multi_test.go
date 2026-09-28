package input

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// Multi copy mode: one copy-mode key drives every pane of the multifocus set.
// These tests build real emulator windows with different output in each, so
// the motions, searches and selections walk real cells, and read back what
// each pane's cursor and selection did and what the yank produced.

// multiPaneWindow is a daemon window holding the given lines, in no mode.
func multiPaneWindow(t *testing.T, id, title string, lines ...string) *terminal.Window {
	t.Helper()
	ptyDataChan := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-ptyDataChan:
			case <-done:
				return
			}
		}
	}()
	t.Cleanup(func() { close(done) })
	win := terminal.NewDaemonWindow(id, title, 0, 0, 80, 24, 0, "pty-"+id, ptyDataChan, config.DefaultScrollbackLines)
	if win == nil {
		t.Fatal("NewDaemonWindow returned nil")
	}
	t.Cleanup(func() { win.Close() })
	win.SetTitle(title)
	win.WriteOutput([]byte(strings.Join(lines, "\r\n") + "\r\n"))
	return win
}

// multiCopyOS is a client with three panes in the multifocus set, holding the
// output three servers would print, and a fourth pane outside the set. The
// first pane is focused. The native clipboard is off (a remote client), so a
// yank's command yields only the OSC 52 write and never touches the machine
// running the tests.
func multiCopyOS(t *testing.T) (*app.OS, []*terminal.Window) {
	t.Helper()
	a := multiPaneWindow(t, "mc-a", "root@node-01",
		"boot ok", "LLDP neighbor: swp1 rack-sw-01", "done")
	b := multiPaneWindow(t, "mc-b", "root@node-02",
		"boot ok", "", "LLDP neighbor: swp2 rack-sw-01", "done")
	c := multiPaneWindow(t, "mc-c", "root@node-03",
		"boot ok", "no carrier", "done")
	d := multiPaneWindow(t, "mc-d", "outside", "LLDP neighbor: swp9 other")
	o := &app.OS{Settings: config.Global, Mode: app.WindowManagementMode, RemoteClient: true}
	o.Windows = []*terminal.Window{a, b, c, d}
	o.FocusedWindow = 0
	o.MultifocusSet = map[string]bool{a.ID: true, b.ID: true, c.ID: true}
	return o, []*terminal.Window{a, b, c, d}
}

// multiKey builds the key a terminal sends for a copy-mode key.
func multiKey(s string) tea.KeyPressMsg {
	switch s {
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	}
	r := []rune(s)[0]
	return tea.KeyPressMsg{Code: r, Text: s}
}

// mcPress sends keys through the real copy-mode entry point and returns the
// last command.
func mcPress(o *app.OS, keys ...string) tea.Cmd {
	var cmd tea.Cmd
	for _, k := range keys {
		fw := o.GetFocusedWindow()
		o.SettleMultiCopy()
		if !fw.InCopyMode() {
			return nil
		}
		_, cmd = HandleCopyModeKey(multiKey(k), o, fw)
	}
	return cmd
}

// typeSearch types a search the way a person does: the slash, the query one
// key at a time, and enter.
func typeSearch(o *app.OS, query string) {
	mcPress(o, "/")
	for _, r := range query {
		mcPress(o, string(r))
	}
	mcPress(o, "enter")
}

// clipboardOf runs a yank's command and returns what it put on the
// clipboard. The command is the OSC 52 write, a bubbletea message whose value
// is the text.
func clipboardOf(t *testing.T, cmd tea.Cmd) string {
	t.Helper()
	if cmd == nil {
		t.Fatal("the yank returned no clipboard command")
	}
	return fmt.Sprint(cmd())
}

func TestCopyModeKeyEntersMultiCopyModeOnTheMultifocusSet(t *testing.T) {
	o, w := multiCopyOS(t)
	o.EnterCopyModeFocused()
	if o.MultiCopy == nil {
		t.Fatal("copy mode with a multifocus set did not enter multi copy mode")
	}
	for _, p := range w[:3] {
		if !p.CopyModeVisible() {
			t.Errorf("pane %s of the multifocus set is not in copy mode", p.ID)
		}
	}
	if w[3].InCopyMode() {
		t.Error("the pane outside the multifocus set entered copy mode")
	}
	mcPress(o, "q")
	if o.MultiCopy != nil {
		t.Error("q did not end multi copy mode")
	}
	for _, p := range w {
		if p.InCopyMode() {
			t.Errorf("pane %s is still in copy mode after q", p.ID)
		}
	}
}

// Without multifocus, the copy-mode key is plain copy mode on one pane.
func TestCopyModeKeyWithoutMultifocusStaysSinglePane(t *testing.T) {
	o, w := multiCopyOS(t)
	o.MultifocusSet = nil
	o.EnterCopyModeFocused()
	if o.MultiCopy != nil {
		t.Fatal("copy mode without multifocus entered multi copy mode")
	}
	if !w[0].CopyModeVisible() || w[1].InCopyMode() || w[2].InCopyMode() {
		t.Fatal("copy mode without multifocus did not stay on the focused pane")
	}
	// Focused outside the set: also single.
	o.MultifocusSet = map[string]bool{w[1].ID: true, w[2].ID: true}
	w[0].ExitCopyMode()
	o.EnterCopyModeFocused()
	if o.MultiCopy != nil || w[1].InCopyMode() {
		t.Fatal("copy mode on a pane outside the multifocus set entered multi copy mode")
	}
}

func TestMultiCopyMotionsMoveEveryPaneInLockstep(t *testing.T) {
	o, w := multiCopyOS(t)
	o.EnterCopyModeFocused()
	mcPress(o, "g", "g", "j", "w", "l")
	// Every pane walked its own cells: the same keys give each the position a
	// single-pane copy mode would reach on that pane.
	for _, p := range w[:3] {
		ref := multiPaneWindow(t, p.ID+"-ref", "ref", paneLines(p)...)
		ref.EnterCopyMode()
		for _, k := range []string{"g", "g", "j", "w", "l"} {
			HandleCopyModeKey(multiKey(k), &app.OS{Settings: config.Global}, ref)
		}
		if p.CopyMode.CursorX != ref.CopyMode.CursorX || p.CopyMode.CursorY != ref.CopyMode.CursorY {
			t.Errorf("pane %s cursor at %d:%d, single-pane copy mode reaches %d:%d",
				p.ID, p.CopyMode.CursorY, p.CopyMode.CursorX, ref.CopyMode.CursorY, ref.CopyMode.CursorX)
		}
		if p.CopyMode.CursorY == 12 {
			t.Errorf("pane %s did not move from where copy mode starts", p.ID)
		}
	}
}

// paneLines reads a pane's screen back as text lines, trailing blanks off.
func paneLines(w *terminal.Window) []string {
	var out []string
	for y := range w.Terminal.Height() {
		out = append(out, strings.TrimRight(extractScreenLineText(w.Terminal, y), " "))
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

func TestMultiCopySearchJumpsEachPaneAndParksMisses(t *testing.T) {
	o, w := multiCopyOS(t)
	o.EnterCopyModeFocused()
	before := *w[2].CopyMode
	typeSearch(o, "lldp")

	if got := w[0].CopyMode.CursorY; got != 1 {
		t.Errorf("pane a cursor on row %d, want its match on row 1", got)
	}
	if got := w[1].CopyMode.CursorY; got != 2 {
		t.Errorf("pane b cursor on row %d, want its own match on row 2", got)
	}
	if w[2].CopyMode.CursorX != before.CursorX || w[2].CopyMode.CursorY != before.CursorY {
		t.Error("the pane with no match moved; it must keep its cursor")
	}
	if !o.MultiCopyParked(w[2].ID) || o.MultiCopyParked(w[0].ID) || o.MultiCopyParked(w[1].ID) {
		t.Fatalf("parked panes wrong: a=%v b=%v c=%v, want only c",
			o.MultiCopyParked(w[0].ID), o.MultiCopyParked(w[1].ID), o.MultiCopyParked(w[2].ID))
	}
	if matched, total := o.MultiCopyMatched(); matched != 2 || total != 3 {
		t.Errorf("matched %d of %d panes, want 2 of 3", matched, total)
	}

	// V selects in the matched panes only, and motions leave the parked one.
	mcPress(o, "V", "j")
	if !w[0].HasSelection() || !w[1].HasSelection() {
		t.Error("V did not select in every matched pane")
	}
	if w[2].HasSelection() || w[2].CopyMode.CursorY != before.CursorY {
		t.Error("the parked pane took the selection or the motion")
	}

	// esc leaves visual mode only, in every pane, parked ones included.
	mcPress(o, "esc")
	for _, p := range w[:3] {
		if !p.InCopyMode() || p.HasSelection() {
			t.Errorf("pane %s after esc in visual: in copy mode %v, selection %v; want copy mode and no selection",
				p.ID, p.InCopyMode(), p.HasSelection())
		}
	}

	// A search that finds something everywhere unparks every pane.
	typeSearch(o, "done")
	if matched, total := o.MultiCopyMatched(); matched != 3 || total != 3 {
		t.Errorf("after a search every pane matches, %d of %d are matched", matched, total)
	}
}

// The focused pane can be the one without a match: the others still lead.
func TestMultiCopyFocusedPaneWithoutMatchDoesNotStopTheOthers(t *testing.T) {
	o, w := multiCopyOS(t)
	o.FocusedWindow = 2
	o.EnterCopyModeFocused()
	typeSearch(o, "lldp")
	mcPress(o, "V")
	if !w[0].HasSelection() || !w[1].HasSelection() || w[2].HasSelection() {
		t.Fatalf("selection a=%v b=%v c=%v, want a and b only",
			w[0].HasSelection(), w[1].HasSelection(), w[2].HasSelection())
	}
	got := clipboardOf(t, mcPress(o, "y"))
	want := "LLDP neighbor: swp1 rack-sw-01\nLLDP neighbor: swp2 rack-sw-01\n"
	if got != want {
		t.Errorf("yank = %q, want %q", got, want)
	}
}

func TestMultiCopyYankTakesEachSelectionInEveryFormat(t *testing.T) {
	o, w := multiCopyOS(t)
	o.EnterCopyModeFocused()

	yank := func() string {
		typeSearch(o, "lldp")
		mcPress(o, "V")
		return clipboardOf(t, mcPress(o, "y"))
	}

	if got, want := yank(), "LLDP neighbor: swp1 rack-sw-01\nLLDP neighbor: swp2 rack-sw-01\n"; got != want {
		t.Errorf("plain yank = %q, want %q", got, want)
	}
	for _, p := range w[:3] {
		if p.HasSelection() {
			t.Errorf("pane %s kept its selection after the yank", p.ID)
		}
	}

	mcPress(o, "tab")
	if o.MultiCopy.Format != config.MultiCopyFormatMarkdown {
		t.Fatalf("tab set the format to %q, want markdown", o.MultiCopy.Format)
	}
	wantMD := "## Pane 0: root@node-01\n\n```\nLLDP neighbor: swp1 rack-sw-01\n```\n\n" +
		"## Pane 1: root@node-02\n\n```\nLLDP neighbor: swp2 rack-sw-01\n```\n"
	if got := yank(); got != wantMD {
		t.Errorf("markdown yank =\n%s\nwant\n%s", got, wantMD)
	}

	mcPress(o, "tab")
	var panes []struct {
		Pane     int      `json:"pane"`
		WindowID string   `json:"window_id"`
		Title    string   `json:"title"`
		Lines    []string `json:"lines"`
	}
	got := yank()
	if err := json.Unmarshal([]byte(got), &panes); err != nil {
		t.Fatalf("json yank is not JSON: %v\n%s", err, got)
	}
	if len(panes) != 2 || panes[0].WindowID != "mc-a" || panes[1].Pane != 1 ||
		panes[1].Title != "root@node-02" || strings.Join(panes[1].Lines, "|") != "LLDP neighbor: swp2 rack-sw-01" {
		t.Errorf("json yank = %s", got)
	}

	mcPress(o, "tab")
	if o.MultiCopy.Format != config.MultiCopyFormatPlain {
		t.Errorf("tab after json set %q, want plain again", o.MultiCopy.Format)
	}
}

// A pane without a selection adds nothing, not even an empty entry; with no
// selection anywhere the yank writes nothing.
func TestMultiCopyYankSkipsPanesWithoutSelection(t *testing.T) {
	o, w := multiCopyOS(t)
	o.EnterCopyModeFocused()
	if cmd := mcPress(o, "y"); cmd != nil {
		t.Errorf("a yank with no selection anywhere wrote %q", clipboardOf(t, cmd))
	}
	mcPress(o, "g", "g", "V")
	// Take pane b out of visual mode by hand: it has no selection now.
	w[1].CopyMode.State = terminal.CopyModeNormal
	got := clipboardOf(t, mcPress(o, "y"))
	if want := "boot ok\nboot ok\n"; got != want {
		t.Errorf("yank = %q, want pane a and pane c only: %q", got, want)
	}
}

func TestMultiCopySaveToFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	o, _ := multiCopyOS(t)
	o.EnterCopyModeFocused()
	typeSearch(o, "lldp")
	mcPress(o, "V", "Y")
	s := o.MultiCopy.Save
	if s == nil {
		t.Fatal("Y did not open the save prompt")
	}
	if !strings.HasPrefix(s.Path, "~/tuios-copy-") || !strings.HasSuffix(s.Path, ".txt") {
		t.Errorf("default path %q, want ~/tuios-copy-<time>.txt", s.Path)
	}
	// tab changes the format and the extension with it.
	mcPress(o, "tab")
	if !strings.HasSuffix(s.Path, ".md") {
		t.Errorf("after tab the path is %q, want the .md extension", s.Path)
	}
	// Type a path of our own.
	for range len(s.Path) {
		mcPress(o, "backspace")
	}
	for _, r := range "~/rack.md" {
		mcPress(o, string(r))
	}
	mcPress(o, "enter")
	if o.MultiCopy.Save != nil {
		t.Fatalf("the prompt is still open after enter: %q", o.MultiCopy.Save.Err)
	}
	data, err := os.ReadFile(filepath.Join(home, "rack.md"))
	if err != nil {
		t.Fatalf("the file was not written: %v", err)
	}
	if !strings.Contains(string(data), "## Pane 0: root@node-01") || !strings.Contains(string(data), "swp2 rack-sw-01") {
		t.Errorf("saved file:\n%s", data)
	}
	if info, _ := os.Stat(filepath.Join(home, "rack.md")); info.Mode().Perm() != 0o600 {
		t.Errorf("saved file mode %v, want 0600", info.Mode().Perm())
	}

	// The same path again: the file is kept and the prompt says why.
	mcPress(o, "V", "Y")
	for range len(o.MultiCopy.Save.Path) {
		mcPress(o, "backspace")
	}
	for _, r := range "~/rack.md" {
		mcPress(o, string(r))
	}
	mcPress(o, "enter")
	if o.MultiCopy.Save == nil || o.MultiCopy.Save.Err == "" {
		t.Fatal("saving over an existing file was not refused")
	}
	mcPress(o, "esc")
	if o.MultiCopy.Save != nil {
		t.Error("esc did not close the prompt")
	}
	again, _ := os.ReadFile(filepath.Join(home, "rack.md"))
	if string(again) != string(data) {
		t.Error("the existing file was changed")
	}
}

// Focus moving off the set ends the mode in every pane.
func TestMultiCopyEndsWhenFocusLeavesTheSet(t *testing.T) {
	o, w := multiCopyOS(t)
	o.EnterCopyModeFocused()
	o.FocusedWindow = 3
	o.SettleMultiCopy()
	if o.MultiCopy != nil {
		t.Fatal("multi copy mode survived focus moving to a pane outside it")
	}
	for _, p := range w[:3] {
		if p.InCopyMode() {
			t.Errorf("pane %s stayed in copy mode", p.ID)
		}
	}
}
