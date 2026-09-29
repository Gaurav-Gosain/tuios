package app

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// scratchOS is a daemon client on session "work", workspace 1, 120x40, with
// one tiled pane and the default [scratch] table.
func scratchOS(t *testing.T) *OS {
	t.Helper()
	m := dockSessionOS(t, 120, true)
	m.SessionName = "work"
	cfg := config.DefaultConfig()
	m.UserConfig = cfg
	return m
}

// addScratchPopup puts a scratch popup for name on workspace ws.
func addScratchPopup(m *OS, name string, ws int) {
	m.Windows = append(m.Windows, &terminal.Window{
		ID: "popup-" + name, IsPopup: true, CustomName: name, Workspace: ws,
		Width: 80, Height: 30,
	})
}

func TestScratchPlanShowsWhenNoPopupIsOpen(t *testing.T) {
	m := scratchOS(t)
	plan := m.planScratch(m.scratchConfig())
	if plan.refuse != "" || !plan.open || len(plan.close) != 0 {
		t.Fatalf("plan = %+v, want an open and nothing else", plan)
	}
}

func TestScratchPlanHidesThePopupOnThisWorkspace(t *testing.T) {
	m := scratchOS(t)
	addScratchPopup(m, "scratch", 1)
	plan := m.planScratch(m.scratchConfig())
	if plan.open || plan.refuse != "" || !slices.Equal(plan.close, []int{1}) {
		t.Fatalf("plan = %+v, want the popup at index 1 closed and no open", plan)
	}
}

// A popup left on another workspace follows the user: it closes there and a
// new one opens here.
func TestScratchPlanMovesThePopupToThisWorkspace(t *testing.T) {
	m := scratchOS(t)
	addScratchPopup(m, "scratch", 2)
	plan := m.planScratch(m.scratchConfig())
	if !plan.open || !slices.Equal(plan.close, []int{1}) {
		t.Fatalf("plan = %+v, want the popup on workspace 2 closed and a new one opened", plan)
	}
}

// A popup of another name, or a pane that is not a popup, is not the
// scratch popup.
func TestScratchPlanIgnoresOtherPopups(t *testing.T) {
	m := scratchOS(t)
	addScratchPopup(m, "picker", 1)
	m.Windows = append(m.Windows, &terminal.Window{ID: "plain", CustomName: "scratch", Workspace: 1})
	plan := m.planScratch(m.scratchConfig())
	if !plan.open || len(plan.close) != 0 {
		t.Fatalf("plan = %+v, want an open and nothing closed", plan)
	}
}

func TestScratchPlanUsesTheConfiguredSession(t *testing.T) {
	m := scratchOS(t)
	m.UserConfig.Scratch.Session = "notes"
	addScratchPopup(m, "scratch", 1)
	addScratchPopup(m, "notes", 1)
	plan := m.planScratch(m.scratchConfig())
	if plan.open || !slices.Equal(plan.close, []int{2}) {
		t.Fatalf("plan = %+v, want only the notes popup closed", plan)
	}
}

func TestScratchPlanRefusals(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*OS)
		want  string
	}{
		{"no daemon", func(m *OS) { m.IsDaemonSession, m.DaemonClient = false, nil }, "needs a daemon session"},
		{"another host", func(m *OS) { m.AttachedHost = "build" }, "only on this machine"},
		{"inside the scratch session", func(m *OS) { m.SessionName = "scratch" }, "This is the scratch session"},
		{"too narrow", func(m *OS) { m.UserConfig.Scratch.Width = "15%" }, "needs 22x8 cells"},
		{"too short", func(m *OS) { m.Height = 9 }, "needs 22x8 cells"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := scratchOS(t)
			tc.setup(m)
			plan := m.planScratch(m.scratchConfig())
			if !strings.Contains(plan.refuse, tc.want) || plan.open || len(plan.close) != 0 {
				t.Fatalf("plan = %+v, want a refusal with %q", plan, tc.want)
			}
		})
	}
}

// The scratch session's own client refuses even when a popup of that name is
// open in it, because the popup would show the session inside itself.
func TestScratchPlanRefusesInsideTheScratchSessionWithAPopup(t *testing.T) {
	m := scratchOS(t)
	m.SessionName = "scratch"
	addScratchPopup(m, "scratch", 1)
	if plan := m.planScratch(m.scratchConfig()); plan.refuse == "" {
		t.Fatalf("plan = %+v, want a refusal", plan)
	}
}

// A second press while the first popup is on its way does nothing, so a
// double press cannot open two popups. After the wait it may ask again.
func TestScratchToggleWaitsForAShowOnItsWay(t *testing.T) {
	m := scratchOS(t)
	var asked []scratchRequest
	prev := scratchOpener
	scratchOpener = func(r scratchRequest) error { asked = append(asked, r); return nil }
	t.Cleanup(func() { scratchOpener = prev })

	cmd := m.ToggleScratch()
	if cmd == nil {
		t.Fatal("the first press asked for no popup")
	}
	if msg, ok := cmd().(ScratchOpenedMsg); !ok || msg.Err != nil || msg.Name != "scratch" {
		t.Fatalf("the open reported %#v", msg)
	}
	if len(asked) != 1 {
		t.Fatalf("asked %d times, want 1", len(asked))
	}
	r := asked[0]
	if r.Outer != "work" || r.Name != "scratch" || r.Width != "80%" || r.Height != "80%" || r.Workspace != 1 {
		t.Fatalf("request = %+v", r)
	}
	if m.ToggleScratch() != nil {
		t.Fatal("a second press opened a second popup while the first was on its way")
	}
	if m.scratchPending != "scratch" {
		t.Fatal("the second press forgot the show on its way")
	}
	m.scratchPendingAt = time.Now().Add(-scratchPendingFor)
	if m.ToggleScratch() == nil {
		t.Fatal("a press after the wait asked for no popup")
	}
}

// The popup that arrives takes the keyboard in terminal mode.
func TestScratchPopupArrivalEntersTerminalMode(t *testing.T) {
	m := scratchOS(t)
	m.Mode = WindowManagementMode
	m.scratchPending, m.scratchPendingAt = "scratch", time.Now()

	m.maybeFocusScratch()
	if m.Mode == TerminalMode || m.scratchPending == "" {
		t.Fatal("terminal mode came before the popup")
	}
	addScratchPopup(m, "scratch", 1)
	m.FocusedWindow = 1
	m.maybeFocusScratch()
	if m.Mode != TerminalMode {
		t.Fatal("the popup arrived and the keyboard stayed in window mode")
	}
	if m.scratchPending != "" {
		t.Fatal("the show stayed on its way after the popup arrived")
	}
}

func TestScratchOpenFailureIsShown(t *testing.T) {
	m := scratchOS(t)
	m.scratchPending, m.scratchPendingAt = "scratch", time.Now()
	m.handleScratchOpened(ScratchOpenedMsg{Name: "scratch", Err: errString("no client")})
	if m.scratchPending != "" {
		t.Fatal("a failed show stayed on its way")
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func TestScratchCommandAttachesAndCreates(t *testing.T) {
	argv := scratchCommand("notes")
	if len(argv) != 4 || !slices.Equal(argv[1:], []string{"attach", "-c", "notes"}) {
		t.Fatalf("argv = %v", argv)
	}
}

// The popup's own size floor must not undercut the scratch floor, or a
// resolved box could pass the check here and still be clamped smaller.
func TestScratchFloorIsAboveThePopupFloor(t *testing.T) {
	if config.ScratchMinWidth < session.PopupMinWidth || config.ScratchMinHeight < session.PopupMinHeight {
		t.Fatal("the scratch floor is below the popup floor")
	}
}
