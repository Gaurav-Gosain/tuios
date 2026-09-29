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
		ID: "popup-" + name, IsPopup: true, IsScratchPopup: true, CustomName: name, Workspace: ws,
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

// A popup the user opened, even one named scratch, and a pane that is not a
// popup are not the scratch popup. Only the daemon's mark makes one.
func TestScratchPlanIgnoresOtherPopups(t *testing.T) {
	m := scratchOS(t)
	m.Windows = append(m.Windows,
		&terminal.Window{ID: "user-popup", IsPopup: true, CustomName: "scratch", Workspace: 1},
		&terminal.Window{ID: "plain", CustomName: "scratch", Workspace: 1})
	plan := m.planScratch(m.scratchConfig())
	if !plan.open || len(plan.close) != 0 {
		t.Fatalf("plan = %+v, want an open and nothing closed", plan)
	}
}

// A hide closes the scratch popup whatever session it shows, so a popup
// opened before the config changed, or with a name the config now refuses,
// can still be hidden.
func TestScratchPlanHidesWhateverTheConfigSays(t *testing.T) {
	m := scratchOS(t)
	addScratchPopup(m, "scratch", 1)
	m.UserConfig.Scratch.Session = "-x"
	plan := m.planScratch(m.scratchConfig())
	if plan.open || plan.refuse != "" || !slices.Equal(plan.close, []int{1}) {
		t.Fatalf("plan = %+v, want the popup closed", plan)
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
		{"name is a flag", func(m *OS) { m.UserConfig.Scratch.Session = "-x" }, `starts with "-"`},
		{"name has a slash", func(m *OS) { m.UserConfig.Scratch.Session = "a/b" }, "path separator"},
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
	if msg, ok := cmd().(ScratchOpenedMsg); !ok || msg.Err != nil {
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
	// The daemon answered, but the popup has not arrived: still on its way.
	m.handleScratchOpened(ScratchOpenedMsg{})
	if m.ToggleScratch() != nil || !m.scratchPending {
		t.Fatal("a press after the daemon's answer, before the popup arrived, asked again")
	}
	// Well past 3 s, still nothing: only the backstop ends it.
	m.scratchPendingAt = time.Now().Add(-5 * time.Second)
	if m.ToggleScratch() != nil {
		t.Fatal("the show on its way ended on a fixed short timer")
	}
	m.scratchPendingAt = time.Now().Add(-scratchPendingMax)
	if m.ToggleScratch() == nil {
		t.Fatal("a press after the wait asked for no popup")
	}
}

// The popup that arrives takes the keyboard in terminal mode.
func TestScratchPopupArrivalEntersTerminalMode(t *testing.T) {
	m := scratchOS(t)
	m.Mode = WindowManagementMode
	m.scratchPending, m.scratchPendingAt = true, time.Now()

	m.maybeFocusScratch()
	if m.Mode == TerminalMode || !m.scratchPending {
		t.Fatal("terminal mode came before the popup")
	}
	addScratchPopup(m, "scratch", 1)
	m.FocusedWindow = 1
	m.maybeFocusScratch()
	if m.Mode != TerminalMode {
		t.Fatal("the popup arrived and the keyboard stayed in window mode")
	}
	if m.scratchPending {
		t.Fatal("the show stayed on its way after the popup arrived")
	}
}

func TestScratchOpenFailureIsShown(t *testing.T) {
	m := scratchOS(t)
	m.scratchPending, m.scratchPendingAt = true, time.Now()
	m.handleScratchOpened(ScratchOpenedMsg{Err: errString("no client")})
	if m.scratchPending {
		t.Fatal("a failed show stayed on its way")
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// The name follows --, so no name is read as a flag.
func TestScratchCommandAttachesAndCreates(t *testing.T) {
	argv := scratchCommand("-notes")
	if !slices.Equal(argv[1:], []string{"attach", "-c", "--hold", "--terminal-mode", "--", "-notes"}) {
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

// tuios attach --terminal-mode enters terminal mode on a session somebody
// already arranged, where [startup] start_in_terminal_mode is not consulted.
func TestForcedTerminalModeOnAnArrangedSession(t *testing.T) {
	m := scratchOS(t)
	m.Mode = WindowManagementMode
	m.sessionUnarranged = false
	m.forceTerminalMode = true
	m.applyStartupPreferences()
	if m.Mode != TerminalMode {
		t.Fatal("--terminal-mode left the client in window mode")
	}
}

// With no pane yet, terminal mode waits for the first one.
func TestForcedTerminalModeWaitsForAPane(t *testing.T) {
	m := scratchOS(t)
	m.Windows = nil
	m.FocusedWindow = -1
	m.Mode = WindowManagementMode
	m.forceTerminalMode = true
	m.UserConfig.Startup.OpenDefaultWindow = false
	m.UserConfig.Startup.Tiled = false
	m.applyStartupPreferences()
	if m.Mode == TerminalMode {
		t.Fatal("terminal mode with no pane to type into")
	}
	m.Windows = []*terminal.Window{{ID: "late", Workspace: 1}}
	m.FocusedWindow = 0
	m.maybeEnterPendingTerminalMode()
	if m.Mode != TerminalMode {
		t.Fatal("the first pane arrived and the client stayed in window mode")
	}
}
