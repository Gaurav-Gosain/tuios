package app

import (
	"errors"
	"fmt"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The scratch popup, after tmux-floax: one key shows a persistent session in a
// popup over the current layout, and the same key hides it again.
//
// The popup is an ordinary popup (os_popup.go) whose command is
// `tuios attach -c <session>`, marked as the scratch popup when the daemon
// opens it (session.WindowState.ScratchPopup). Hiding it closes the popup,
// which ends that attach and so detaches one client. The session is the
// daemon's and keeps running, so the next show attaches it again and it is
// exactly as it was.
//
// Closing rather than hiding the popup is deliberate. A hidden popup is a pane
// every peer of this session still holds: it would sit in the window set, the
// dock and the saved state, and its client would stay attached to the scratch
// session at the popup's last size while nobody looks at it. A closed popup
// leaves nothing behind, it opens on whichever workspace the user is on when
// they press the key, and the reattach it costs is one local round trip.
//
// The popup is the session's, like every popup, so a toggle on another client
// of the same session closes it for everyone.
//
// The key reaches this client even while the popup has the focus. A binding is
// looked up here before a key is passed to the pane, so the inner client never
// sees the toggle, and pressing it from inside the popup hides the popup.

// scratchPendingMax is the backstop on a show that is on its way. A show is
// on its way from the press until the daemon refuses it or the popup arrives,
// and a second press in that time does nothing, so a double press cannot open
// two popups. The backstop only matters when the daemon never answers.
const scratchPendingMax = 10 * time.Second

// ScratchOpenedMsg reports the outcome of the call that opens the scratch
// popup.
type ScratchOpenedMsg struct {
	Err error
}

// scratchOpener opens the popup through the daemon. Tests replace it.
var scratchOpener = openScratchPopup

// scratchConfig is the [scratch] table in force.
func (m *OS) scratchConfig() config.ScratchConfig {
	if m.UserConfig == nil {
		return config.ScratchConfig{}
	}
	return m.UserConfig.Scratch
}

// isScratchPopup reports whether w is a popup toggle_scratch opened. The mark
// is the daemon's, so a popup the user opened with the same name is not one.
func isScratchPopup(w *terminal.Window) bool {
	return w != nil && w.IsPopup && w.IsScratchPopup
}

// scratchPlan is what one press of the toggle does. It is worked out apart
// from doing it, so a test can read the decision without a daemon.
type scratchPlan struct {
	// refuse is the warning to show instead of acting, or "".
	refuse string
	// close are the indexes of the scratch popups to close, highest first.
	close []int
	// open asks the daemon for a popup on the current workspace.
	open bool
}

// planScratch decides what the toggle does now.
//
// A scratch popup on the current workspace means hide: every scratch popup
// closes. Otherwise it means show, and a popup left on another workspace
// closes as the new one opens here, so the popup follows the user.
func (m *OS) planScratch(cfg config.ScratchConfig) scratchPlan {
	name := cfg.SessionName()
	switch {
	case !m.IsDaemonSession || m.DaemonClient == nil:
		return scratchPlan{refuse: "The scratch popup needs a daemon session. Start tuios with the daemon to use it."}
	case m.AttachedHost != "":
		return scratchPlan{refuse: "The scratch popup works only on this machine. Switch to a session on this machine to use it."}
	case m.SessionName == name:
		// The popup would show this session inside itself, which the
		// daemon refuses (see session/nested_attach.go).
		return scratchPlan{refuse: "This is the scratch session. Open the scratch popup from a different session."}
	}

	var plan scratchPlan
	here := false
	for i := len(m.Windows) - 1; i >= 0; i-- {
		w := m.Windows[i]
		if !isScratchPopup(w) {
			continue
		}
		here = here || w.Workspace == m.CurrentWorkspace
		plan.close = append(plan.close, i)
	}
	// A hide needs nothing from the config, so a bad name never traps a
	// popup on the screen.
	if here {
		return plan
	}
	if m.scratchPending && time.Since(m.scratchPendingAt) < scratchPendingMax {
		return scratchPlan{}
	}
	if problem := config.ScratchNameProblem(name); problem != "" {
		return scratchPlan{refuse: problem + " Set a different [scratch] session name."}
	}

	boxW := session.ResolvePopupSize(cfg.WidthSpec(), session.PopupDefaultWidth, m.GetContentWidth(), session.PopupMinWidth)
	boxH := session.ResolvePopupSize(cfg.HeightSpec(), session.PopupDefaultHeight, m.GetUsableHeight(), session.PopupMinHeight)
	if boxW < config.ScratchMinWidth || boxH < config.ScratchMinHeight {
		return scratchPlan{refuse: scratchTooSmallMessage()}
	}
	plan.open = true
	return plan
}

// ToggleScratch shows the scratch session in a popup on the current
// workspace, or hides the popup when it is already there.
func (m *OS) ToggleScratch() tea.Cmd {
	cfg := m.scratchConfig()
	plan := m.planScratch(cfg)
	if plan.refuse != "" {
		m.ShowNotification(plan.refuse, "warning", m.Settings.NotificationDuration)
		return nil
	}
	// Highest index first, so an index still names its window when the close
	// is local and removes it at once.
	for _, i := range plan.close {
		m.DeleteWindow(i)
	}
	if !plan.open {
		// A hide ends any show still on its way. A press that did nothing
		// (a show is already on its way) leaves it alone.
		if len(plan.close) > 0 {
			m.scratchPending = false
		}
		return nil
	}

	m.scratchPending = true
	m.scratchPendingAt = time.Now()
	req := scratchRequest{
		Outer:     m.SessionName,
		Name:      cfg.SessionName(),
		Width:     cfg.WidthSpec(),
		Height:    cfg.HeightSpec(),
		Workspace: m.CurrentWorkspace,
	}
	return func() tea.Msg {
		return ScratchOpenedMsg{Err: scratchOpener(req)}
	}
}

// scratchTooSmallMessage is the warning for a popup box below the floor.
func scratchTooSmallMessage() string {
	return fmt.Sprintf("The scratch popup needs %dx%d cells. Make the terminal or the [scratch] size larger.",
		config.ScratchMinWidth, config.ScratchMinHeight)
}

// scratchRequest is what the popup call needs, copied off the model so the
// call can run off the update goroutine.
type scratchRequest struct {
	Outer         string
	Name          string
	Width, Height string
	Workspace     int
}

// scratchCommand is the popup's argv: this binary, attaching the scratch
// session. openScratchPopup has made the session already. -c still creates
// it if it was killed between the two calls.
//
// --hold keeps a failed attach on the screen until enter, where it would
// otherwise close the popup before anyone read why. --terminal-mode puts the
// keyboard in the session's pane, since typing is what the popup is for.
// The name comes after --, so no name is read as a flag.
func scratchCommand(name string) []string {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		exe = "tuios"
	}
	return []string{exe, "attach", "-c", "--hold", "--terminal-mode", "--", name}
}

// openScratchPopup asks the daemon for the popup. It runs as a command,
// never from Update, for the reason labelVerbCmd gives.
func openScratchPopup(req scratchRequest) error {
	c, err := session.DialVerbClient()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	// The session is made here, with a first window, rather than by the
	// attach: a session the attach creates has no window unless
	// [startup] open_default_window is on, and the popup would show an
	// empty session with nothing to type into.
	if _, err := c.Call("new-session", map[string]any{"name": req.Name}); err != nil {
		if call, ok := errors.AsType[*session.VerbCallError](err); !ok || call.Code != session.ErrVerbSessionExists {
			return err
		}
	}
	_, err = c.Call("popup", map[string]any{
		"session":   req.Outer,
		"name":      req.Name,
		"width":     req.Width,
		"height":    req.Height,
		"workspace": req.Workspace,
		"scratch":   true,
		"command":   scratchCommand(req.Name),
	})
	return err
}

// handleScratchOpened reports a failed show and ends it. A show the daemon
// accepted stays on its way until its popup arrives in a state push, where
// maybeFocusScratch takes it: until then this client does not hold the
// popup, and a second press would open another.
func (m *OS) handleScratchOpened(msg ScratchOpenedMsg) {
	if msg.Err == nil {
		return
	}
	m.scratchPending = false
	m.ShowNotification("The scratch popup did not open: "+msg.Err.Error(), "error", m.Settings.NotificationDuration)
}

// maybeFocusScratch ends the show on its way once its popup arrives, and puts
// the keyboard in the popup, so the user can type into the session at once.
func (m *OS) maybeFocusScratch() {
	if !m.scratchPending {
		return
	}
	if time.Since(m.scratchPendingAt) >= scratchPendingMax {
		m.scratchPending = false
		return
	}
	if !m.hasFocusedWindow() || !isScratchPopup(m.Windows[m.FocusedWindow]) {
		return
	}
	m.scratchPending = false
	if m.Mode != TerminalMode {
		m.EnterTerminalMode()
	}
}
