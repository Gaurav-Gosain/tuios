package input

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/adrg/xdg"
)

// hoverOS builds a model with the rail on the left, its footer controls
// available, and two panes beside it. Hover is a colour change and nothing
// else, so the assertions below read colours out of the composed frame.
func hoverOS(t *testing.T) *app.OS {
	t.Helper()
	app.SetInputHandler(HandleInput)

	// NewOS below reads and the drag tests write the sidebar state file, so the
	// state home has to be scratch: a dragged rail width persisted by one run
	// moved the rail's edge for the next (`go test -count=2` failed on exactly
	// that), and it also wrote into the developer's real state dir. Cleanup is
	// registered before t.Setenv so the reload back runs after the env is
	// restored (cleanups are LIFO).
	t.Cleanup(xdg.Reload)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	xdg.Reload()

	pe, pp, pw := config.Global.SidebarEnabled, config.Global.SidebarPosition, config.Global.SidebarWidth
	config.Global.SidebarEnabled, config.Global.SidebarPosition, config.Global.SidebarWidth = true, "left", 30
	t.Cleanup(func() {
		config.Global.SidebarEnabled, config.Global.SidebarPosition, config.Global.SidebarWidth = pe, pp, pw
	})

	cfg := config.DefaultConfig()
	m := app.NewOS(app.OSOptions{UserConfig: cfg, KeybindRegistry: config.NewKeybindRegistry(cfg)})
	m.Width, m.Height = 120, 40
	m.EffectiveWidth, m.EffectiveHeight = 120, 40
	m.Windows = []*terminal.Window{
		{ID: "aaaaaaaa1111", CustomName: "editor", X: 31, Y: 1, Width: 40, Height: 20, Workspace: 1},
		{ID: "bbbbbbbb2222", CustomName: "logs", X: 75, Y: 1, Width: 40, Height: 20, Workspace: 1},
	}
	m.CurrentWorkspace, m.FocusedWindow = 1, 0

	// The footer's new-session control only exists when sessions can be made.
	client := session.NewTUIClient()
	client.UpdateSessionCache([]session.SessionInfo{{Name: "local"}})
	m.DaemonClient = client
	m.SessionName = "local"
	return m
}

// frameLines composes a frame the way the program does and splits it into rows.
func frameLines(m *app.OS) []string {
	return strings.Split(m.View().Content, "\n")
}

// railCell locates a label inside the rail's column band and returns the cell to
// point at. Searching the whole row would find a pane's title bar instead.
func railCell(t *testing.T, lines []string, label string) (x, y int) {
	t.Helper()
	for i, line := range lines {
		plain := stripSGR(line)
		idx := strings.Index(plain, label)
		if idx >= 0 && idx < config.Global.SidebarWidth {
			return idx, i
		}
	}
	t.Fatalf("no %q in the rail:\n%s", label, strings.Join(lines, "\n"))
	return 0, 0
}

// stripSGR drops escape sequences so a rendered row can be searched by text.
func stripSGR(s string) string {
	var b strings.Builder
	esc := false
	for i := range len(s) {
		c := s[i]
		switch {
		case esc:
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
				esc = false
			}
		case c == 0x1b:
			esc = true
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// motion delivers one motion event through the real Update path.
func motion(m *app.OS, x, y int) *app.OS {
	next, _ := m.Update(tea.MouseMotionMsg{X: x, Y: y})
	return next.(*app.OS)
}

// TestFocusFollowsMouseInTerminalMode is the same root cause seen from the
// other side: the setting worked all along, the events just never arrived while
// a pane held focus.
func TestFocusFollowsMouseInTerminalMode(t *testing.T) {
	prev := config.Global.FocusFollowsMouse
	config.Global.FocusFollowsMouse = true
	t.Cleanup(func() { config.Global.FocusFollowsMouse = prev })

	m := hoverOS(t)
	m.Mode = app.TerminalMode
	if m.View().MouseMode != tea.MouseModeAllMotion {
		t.Fatal("terminal mode does not ask the host for button-free motion, so focus can never follow")
	}

	m = motion(m, 80, 5) // inside the second pane
	if m.FocusedWindow != 1 {
		t.Errorf("focus did not follow the pointer in terminal mode (focused=%d)", m.FocusedWindow)
	}

	// The rail is chrome, so pointing at it must not hand a pane focus.
	m = motion(m, 3, 5)
	if m.FocusedWindow != 1 {
		t.Errorf("the rail stole pane focus (focused=%d)", m.FocusedWindow)
	}
}
