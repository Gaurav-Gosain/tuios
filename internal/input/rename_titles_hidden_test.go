package input

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// Hidden titles used to veto every rename entry point, because rename once
// edited the title bar in place. It is a centred dialog now, so it draws its own
// frame and owes nothing to the bar. These pin the three ways in against the
// rendered frame, since "the state says Renaming" is not the same claim as "the
// user can see an editor".

// hiddenTitlesOS is a two-pane client drawn with no window titles at all.
func hiddenTitlesOS(t *testing.T) *app.OS {
	t.Helper()
	prev := config.WindowTitlePosition
	config.WindowTitlePosition = "hidden"
	t.Cleanup(func() { config.WindowTitlePosition = prev })
	return twoPaneOS(t)
}

// renameDialogOnScreen reports whether the frame is actually showing an editor.
func renameDialogOnScreen(o *app.OS) bool {
	return strings.Contains(o.View().Content, o.RenameDialogTitle())
}

func TestPrefixRenameOpensWithTitlesHidden(t *testing.T) {
	o := hiddenTitlesOS(t)
	o.FocusedWindow = 1

	o, _ = handlePrefixRenameWindow(press("r"), o)

	if !o.Renaming() {
		t.Fatal("the prefix rename refused with titles hidden")
	}
	if o.RenameKind != app.RenameWindow || o.RenameTargetID != o.Windows[1].ID {
		t.Fatalf("rename = {kind:%v target:%q}, want the focused pane", o.RenameKind, o.RenameTargetID)
	}
	if !renameDialogOnScreen(o) {
		t.Error("the editor is open but the frame draws no dialog")
	}
}

func TestDirectRenameKeyOpensWithTitlesHidden(t *testing.T) {
	// The rail is off as well, since it was the only reason the direct key
	// still worked with titles hidden.
	prev := config.SidebarEnabled
	config.SidebarEnabled = false
	t.Cleanup(func() { config.SidebarEnabled = prev })

	o := hiddenTitlesOS(t)
	o.FocusedWindow = 0

	o, _ = handleRenameWindow(press("r"), o)

	if !o.Renaming() {
		t.Fatal("the window-mode rename key refused with titles hidden and no rail")
	}
	if !renameDialogOnScreen(o) {
		t.Error("the editor is open but the frame draws no dialog")
	}

	// The dialog owns the keyboard from here, whatever the title bar is doing.
	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: 'z', Text: "z"}, o)
	if !strings.HasSuffix(o.RenameBuffer, "z") {
		t.Errorf("rename buffer = %q, want the typed key appended", o.RenameBuffer)
	}
}

func TestPaletteRenameOpensWithTitlesHidden(t *testing.T) {
	o := hiddenTitlesOS(t)
	o.FocusedWindow = 1

	o = runPaletteCommandForTest(t, o, "Rename window")

	if !o.Renaming() {
		t.Fatal("the palette's rename row did nothing with titles hidden")
	}
	if !renameDialogOnScreen(o) {
		t.Error("the editor is open but the frame draws no dialog")
	}
}

// runPaletteCommandForTest runs a command-palette row by name.
func runPaletteCommandForTest(t *testing.T, o *app.OS, name string) *app.OS {
	t.Helper()
	for _, c := range app.GetCommandPaletteItems() {
		if c.Name == name {
			out, _ := c.Action(o)
			return out
		}
	}
	t.Fatalf("no palette command named %q", name)
	return o
}
