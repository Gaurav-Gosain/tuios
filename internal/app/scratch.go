package app

import (
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
// `tuios attach -c <session>`. Hiding it closes the popup, which ends that
// attach and so detaches one client. The session is the daemon's and keeps
// running, so the next show attaches it again and it is exactly as it was.
//
// Closing rather than hiding the popup is deliberate. A hidden popup is a pane
// every peer of this session still holds: it would sit in the window set, the
// dock and the saved state, and its client would stay attached to the scratch
// session at the popup's last size while nobody looks at it. A closed popup
// leaves nothing behind, it opens on whichever workspace the user is on when
// they press the key, and the reattach it costs is one local round trip.
//
// The key reaches this client even while the popup has the focus. A binding is
// looked up here before a key is passed to the pane, so the inner client never
// sees the toggle, and pressing it from inside the popup hides the popup.

// scratchPendingFor is how long a show waits for its popup before a second
// press may ask again. It stops a double press from opening two popups.
const scratchPendingFor = 3 * time.Second

// ScratchOpenedMsg reports the outcome of the call that opens the scratch
// popup.
type ScratchOpenedMsg struct {
	Name string
	Err  error
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

// isScratchPopup reports whether w is the popup that shows the scratch
// session name.
func isScratchPopup(w *terminal.Window, name string) bool {
	return w != nil && w.IsPopup && w.CustomName == name
}

// scratchPlan is what one press of the toggle does. It is worked out apart
// from doing it, so a test can read the decision without a daemon.
type scratchPlan struct {
	// refuse is the warning to show instead of acting, or "".
	refuse string
	// close are the indexes of the scratch popups to close.
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
		if !isScratchPopup(w, name) {
			continue
		}
		here = here || w.Workspace == m.CurrentWorkspace
		plan.close = append(plan.close, i)
	}
	if here {
		return plan
	}
	if m.scratchPending == name && time.Since(m.scratchPendingAt) < scratchPendingFor {
		return scratchPlan{}
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
			m.scratchPending = ""
		}
		return nil
	}

	name := cfg.SessionName()
	m.scratchPending = name
	m.scratchPendingAt = time.Now()
	req := scratchRequest{
		Outer:     m.SessionName,
		Name:      name,
		Width:     cfg.WidthSpec(),
		Height:    cfg.HeightSpec(),
		Workspace: m.CurrentWorkspace,
	}
	return func() tea.Msg {
		return ScratchOpenedMsg{Name: name, Err: scratchOpener(req)}
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
// session and creating it when it does not exist yet.
func scratchCommand(name string) []string {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		exe = "tuios"
	}
	return []string{exe, "attach", "-c", name}
}

// openScratchPopup asks the daemon for the popup. It runs as a command,
// never from Update, for the reason labelVerbCmd gives.
func openScratchPopup(req scratchRequest) error {
	c, err := session.DialVerbClient()
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	_, err = c.Call("popup", map[string]any{
		"session":   req.Outer,
		"name":      req.Name,
		"width":     req.Width,
		"height":    req.Height,
		"workspace": req.Workspace,
		"command":   scratchCommand(req.Name),
	})
	return err
}

// handleScratchOpened reports a failed show. A popup that did open arrives in
// a state push, where maybeFocusScratch takes it.
func (m *OS) handleScratchOpened(msg ScratchOpenedMsg) {
	if msg.Err == nil {
		return
	}
	if m.scratchPending == msg.Name {
		m.scratchPending = ""
	}
	m.ShowNotification("The scratch popup did not open: "+msg.Err.Error(), "error", m.Settings.NotificationDuration)
}

// maybeFocusScratch puts the keyboard in the scratch popup once it arrives,
// so the user can type into the session at once.
func (m *OS) maybeFocusScratch() {
	if m.scratchPending == "" {
		return
	}
	if time.Since(m.scratchPendingAt) >= scratchPendingFor {
		m.scratchPending = ""
		return
	}
	if !m.hasFocusedWindow() || !isScratchPopup(m.Windows[m.FocusedWindow], m.scratchPending) {
		return
	}
	m.scratchPending = ""
	if m.Mode != TerminalMode {
		m.EnterTerminalMode()
	}
}
