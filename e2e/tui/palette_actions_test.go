package tuie2e

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestAPaletteRowIsAnActionAKeyCanRun: the command palette's rows that had
// code of their own and no action (the theme picker, the layout switches,
// the mailbox, and seventeen more) are actions now, so a key in config.toml
// can run one. Before, theme_picker was a name nothing knew: a binding to it
// did nothing, and only the palette row opened the picker.
//
// How this could pass wrongly, written down first:
//   - The key could open the picker some other way. Y has no default binding
//     in the prefix table, so only the binding in the file can act on it.
//   - The picker could be open already. The test checks it is not before the
//     key, then waits for it after.
//   - The palette's row could still run its own code and not the action. The
//     second half opens the row from the palette and sees the same picker.
func TestAPaletteRowIsAnActionAKeyCanRun(t *testing.T) {
	base := t.TempDir()
	writeConfig(t, base, "[keybindings.prefix_mode]\ntheme_picker = [\"Y\"]\n")
	term := startIn(t, base, startOpts{})
	waitBoot(t, term)

	if pickerShown(term.Screen()) {
		t.Fatalf("the theme picker is open before any key:\n%s", term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Ctrl('b'), "Y"); err != nil {
		t.Fatalf("send prefix+Y: %v", err)
	}
	if err := term.WaitFor(pickerShown, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: prefix+Y bound to theme_picker did not open the theme picker: %v\n%s", err, term.Snapshot())
	}
	saveArtifact(t, term, artifactDir(t), "theme-picker-by-key")
	if err := term.SendKeys(tuitest.Esc); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool { return !pickerShown(s) }, uiTimeout); err != nil {
		t.Fatalf("the theme picker did not close: %v\n%s", err, term.Snapshot())
	}

	if err := term.SendKeys(tuitest.Ctrl('b'), "P"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText(paletteTitle, uiTimeout); err != nil {
		t.Fatalf("the palette did not open: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys("Theme picker"); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("Theme picker", uiTimeout); err != nil {
		t.Fatalf("the palette has no Theme picker row: %v\n%s", err, term.Snapshot())
	}
	if err := term.SendKeys(tuitest.Enter); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitFor(pickerShown, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the palette's Theme picker row did not open the picker: %v\n%s", err, term.Snapshot())
	}
	alive(t, term, "after the theme picker from a key and from the palette")
}

// pickerShown reports whether the theme picker is on screen, by the list
// heading it draws.
func pickerShown(s tuitest.Screen) bool {
	return strings.Contains(s.Text(), "themes")
}
