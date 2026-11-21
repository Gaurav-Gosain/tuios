package input

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/adrg/xdg"
)

func hoverOS(t *testing.T) *app.OS {
	t.Helper()
	app.SetInputHandler(HandleInput)

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

	client := session.NewTUIClient()
	client.UpdateSessionCache([]session.SessionInfo{{Name: "local"}})
	m.DaemonClient = client
	m.SessionName = "local"
	return m
}

func frameLines(m *app.OS) []string {
	if m.PointerMotionPending() {
		_, _ = m.Update(app.TickerMsg(time.Now()))
	}
	return strings.Split(m.View().Content, "\n")
}

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

	m = motion(m, 80, 5)
	if m.FocusedWindow != 1 {
		t.Errorf("focus did not follow the pointer in terminal mode (focused=%d)", m.FocusedWindow)
	}

	m = motion(m, 3, 5)
	if m.FocusedWindow != 1 {
		t.Errorf("the rail stole pane focus (focused=%d)", m.FocusedWindow)
	}
}
