package input

import (
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// filesRailOS gives the rail the keyboard with the files section listing dir
// and the cursor on name.
func filesRailOS(t *testing.T, dir, name string) *app.OS {
	t.Helper()
	prev, prevActions := config.Global.SidebarEnabled, config.Global.SidebarFileActions
	config.Global.SidebarEnabled = true
	config.Global.SidebarFileActions = true
	t.Cleanup(func() {
		config.Global.SidebarEnabled = prev
		config.Global.SidebarFileActions = prevActions
	})

	o := twoPaneOS(t)
	o.SessionName = "main"
	o.IsDaemonSession = true
	o.EnterSidebarFocus()
	if !o.OpenFileView(dir) {
		t.Fatal("the rail refused to open the files section")
	}
	// The listing arrives as a message, exactly as it does in the client.
	if cmd := o.TakeSidebarCmd(); cmd != nil {
		if _, c := o.Update(cmd()); c != nil {
			_ = c
		}
	}
	_ = o.View() // the rail publishes its rows as it draws
	if name == "" {
		return o
	}
	if !railCursorToFileRow(t, o, name) {
		t.Fatalf("the rail drew no listing row for %q", name)
	}
	return o
}

// railCursorToFileRow parks the cursor on the listing row whose drawn text
// holds name. The nav rows carry no file name, so the row is found through the
// hit rectangles the render published, which is the same identity a click uses.
func railCursorToFileRow(t *testing.T, o *app.OS, name string) bool {
	t.Helper()
	for i := range o.SidebarNav {
		o.SidebarCursor = i
		if !o.SidebarCursorOnFile() {
			continue
		}
		// The cursor row is the one drawn with the rail's selection, so ask the
		// model what it would act on instead of reading pixels.
		if o.FileActionTargetNameForTest() == name {
			return true
		}
	}
	return false
}
