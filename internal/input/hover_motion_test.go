package input

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
	"github.com/adrg/xdg"
	"github.com/charmbracelet/colorprofile"
)

// hoverOS builds a model with the rail on the left, its footer controls
// available, and two panes beside it. Hover is a colour change and nothing
// else, so the writer needs a colour profile or every frame renders identical
// and the assertions below cannot see the thing they are about.
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

	prevProfile := lipgloss.Writer.Profile
	lipgloss.Writer.Profile = colorprofile.TrueColor
	pe, pp, pw := config.SidebarEnabled, config.SidebarPosition, config.SidebarWidth
	config.SidebarEnabled, config.SidebarPosition, config.SidebarWidth = true, "left", 30
	t.Cleanup(func() {
		lipgloss.Writer.Profile = prevProfile
		config.SidebarEnabled, config.SidebarPosition, config.SidebarWidth = pe, pp, pw
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
		if idx >= 0 && idx < config.SidebarWidth {
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

// TestMotionPaintsHoverOnTheNextFrame is the regression the rail's hover kept
// failing: one motion event onto a hover target, on a model with nothing else
// happening, must show up on the very next composed frame. It asserts against
// the frame rather than the hover fields because the fields were always right;
// the screen was the thing that did not follow.
//
// Terminal mode is covered because that is where the bug lived: the view asked
// the host for button-event tracking whenever a pane held focus, so motion with
// no button held never arrived at all.
func TestMotionPaintsHoverOnTheNextFrame(t *testing.T) {
	targets := []struct {
		name  string
		label string
	}{
		// The add control is a "+" on the sessions header now, so the row the
		// pointer lands on is that header rather than the footer.
		{"sessions header add control", "+"},
		{"rail window row", "editor"},
	}
	modes := []struct {
		name string
		mode app.Mode
	}{
		{"window management", app.WindowManagementMode},
		{"terminal", app.TerminalMode},
	}

	for _, tc := range targets {
		for _, md := range modes {
			t.Run(tc.name+"/"+md.name, func(t *testing.T) {
				m := hoverOS(t)
				m.Mode = md.mode

				idle := frameLines(m)
				x, y := railCell(t, idle, tc.label)

				hovered := frameLines(motion(m, x, y))
				if len(hovered) != len(idle) {
					t.Fatalf("frame changed height: %d rows, was %d", len(hovered), len(idle))
				}
				if hovered[y] == idle[y] {
					t.Errorf("row %d is unchanged after the pointer landed on %q; hover never reached the screen", y, tc.label)
				}
				if !strings.Contains(stripSGR(hovered[y]), tc.label) {
					t.Errorf("row %d no longer holds %q: %q", y, tc.label, stripSGR(hovered[y]))
				}
			})
		}
	}
}

// TestViewAlwaysRequestsAllMotion pins the host tracking mode. Button-event
// tracking reports motion only while a button is held, so anything less than
// all-motion means the hover affordances and focus-follows-mouse never see the
// pointer move. A guest's own mouse mode has no say in this; it governs
// forwarding, not what the host reports to tuios.
func TestViewAlwaysRequestsAllMotion(t *testing.T) {
	m := hoverOS(t)

	cases := []struct {
		name  string
		setup func()
	}{
		{"window management", func() { m.Mode = app.WindowManagementMode }},
		{"terminal, guest with no mouse mode", func() { m.Mode = app.TerminalMode }},
		{"terminal, guest tracking clicks only", func() {
			em := vt.NewEmulator(38, 18)
			t.Cleanup(func() { _ = em.Close() })
			if _, err := em.Write([]byte("\x1b[?1000h")); err != nil {
				t.Fatalf("enable mouse tracking: %v", err)
			}
			m.Windows[0].Terminal = em
			m.Mode = app.TerminalMode
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup()
			if got := m.View().MouseMode; got != tea.MouseModeAllMotion {
				t.Errorf("mouse mode = %v, want all-motion", got)
			}
		})
	}
}

// TestTrailingMotionIsNotDropped covers the pointer coming to rest. The last
// motion of a sweep is the one that decides what the user is hovering, so a
// burst ending on a row must leave exactly the frame a single move to that row
// would have left. Anything that coalesced the sweep and dropped its tail would
// paint the row the pointer passed through instead.
func TestTrailingMotionIsNotDropped(t *testing.T) {
	direct := hoverOS(t)
	// The footer's toggle, which is the rail's bottom line: far enough down that
	// the sweep to it crosses most of the rail.
	x, y := railCell(t, frameLines(direct), "«")
	want := frameLines(motion(direct, x, y))

	swept := hoverOS(t)
	_ = frameLines(swept)
	for row := 2; row < y; row++ {
		swept = motion(swept, x, row)
	}
	got := frameLines(motion(swept, x, y))

	if got[y] != want[y] {
		t.Errorf("row %d after a sweep differs from a single move to it:\n got %q\nwant %q",
			y, stripSGR(got[y]), stripSGR(want[y]))
	}
}

// TestFocusFollowsMouseInTerminalMode is the same root cause seen from the
// other side: the setting worked all along, the events just never arrived while
// a pane held focus.
func TestFocusFollowsMouseInTerminalMode(t *testing.T) {
	prev := config.FocusFollowsMouse
	config.FocusFollowsMouse = true
	t.Cleanup(func() { config.FocusFollowsMouse = prev })

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

// TestGuestMotionForwardingFollowsGuestMouseMode guards what the host's
// all-motion tracking must not leak: a guest sees only the motion its own mode
// would have reported.
func TestGuestMotionForwardingFollowsGuestMouseMode(t *testing.T) {
	cases := []struct {
		name   string
		enable string
		button tea.MouseButton
		want   bool
	}{
		{"any-event, button free", "\x1b[?1003h", tea.MouseNone, true},
		{"any-event, button held", "\x1b[?1003h", tea.MouseLeft, true},
		{"button-event, button free", "\x1b[?1002h", tea.MouseNone, false},
		{"button-event, button held", "\x1b[?1002h", tea.MouseLeft, true},
		{"normal tracking, button free", "\x1b[?1000h", tea.MouseNone, false},
		{"normal tracking, button held", "\x1b[?1000h", tea.MouseLeft, false},
		{"no mouse mode", "", tea.MouseLeft, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			em := vt.NewEmulator(38, 18)
			t.Cleanup(func() { _ = em.Close() })
			if tc.enable != "" {
				if _, err := em.Write([]byte(tc.enable)); err != nil {
					t.Fatalf("enable mouse tracking: %v", err)
				}
			}
			if got := guestWantsMotion(em, tc.button); got != tc.want {
				t.Errorf("forward = %v, want %v", got, tc.want)
			}
		})
	}
}
