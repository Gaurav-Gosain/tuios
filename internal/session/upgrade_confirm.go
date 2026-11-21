package session

import (
	"fmt"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testknob"
)

const upgradeConfirmWait = 60 * time.Second

// UpgradeConfirmWait is upgradeConfirmWait, for the CLI's call timeout.
const UpgradeConfirmWait = upgradeConfirmWait

const (
	upgradeAnswerNo  = "No"
	upgradeAnswerYes = "Yes"
)

// UpgradeAsk is what the upgrade dialog shows besides the asking pane: the
// binary that would run as the daemon, by its full path, and the version it
// says it is.
type UpgradeAsk struct {
	Binary  string `json:"binary"`
	Version string `json:"version"`
	// CloseExited names the exited panes on other machines the upgrade
	// closes (session: name (id) on machine), so it does not happen unasked.
	CloseExited []string `json:"close_exited,omitempty"`
}

func upgradeConfirmTimeout() time.Duration {
	if handoffTestOn() {
		if d := testknob.Millis("upgrade_confirm_ms"); d > 0 {
			return d
		}
	}
	return upgradeConfirmWait
}

func (d *Daemon) callerIsPlugin(cs *connState) bool {
	return cs != nil && (cs.pluginBound.Load() != nil || d.pluginOfPeer(cs) != nil)
}

func (d *Daemon) personClients() int {
	var attached []*connState
	d.clientsMu.RLock()
	for _, cs := range d.clients {
		cs.mu.Lock()
		if cs.attached && cs.isTUIClient && cs.upgradeDialog && !cs.viaLink && !cs.viewOnly {
			attached = append(attached, cs)
		}
		cs.mu.Unlock()
	}
	d.clientsMu.RUnlock()
	n := 0
	for _, cs := range attached {
		if d.mayActAsHuman(cs) {
			n++
		}
	}
	return n
}

func (d *Daemon) confirmUpgrade(cs *connState, bin handoffBinary, closeExited []string) string {
	refuse := func(why string) string {
		LogBasic("Live upgrade from a pane refused: %s", why)
		return why
	}
	if why := d.upgradeDialogRefusal(); why != "" {
		return refuse(why)
	}
	if !askLinePrintable(bin.path) || !askLineShown(bin.accepts.Version, askMaxOption) {
		return refuse("The binary's path or version cannot be shown in the dialog as it is. Ask the person to run tuios daemon upgrade from a terminal outside tuios")
	}
	item := AttentionItem{
		Name:    "a process inside a pane",
		Summary: "Upgrade the daemon to " + bin.accepts.Version + "?",
		Options: []string{upgradeAnswerNo, upgradeAnswerYes},
		Upgrade: &UpgradeAsk{Binary: bin.path, Version: bin.accepts.Version, CloseExited: closeExited},
	}
	_, window := d.peerPane(cs)
	for _, sess := range d.manager.AllSessions() {
		if item.Session == "" {
			item.Session = sess.Name()
		}
		st := sess.GetState()
		if idx, err := findWindowStateIndex(st.Windows, window); window != "" && err == nil {
			w := st.Windows[idx]
			item.Session, item.Window, item.Name = sess.Name(), w.ID, windowLabelOf(w)
			item.Harness, item.Workspace = w.AgentHarness, w.Workspace
			break
		}
	}
	hold, err := d.attention.openAsk(item, true)
	if err != nil {
		return refuse(fmt.Sprintf("The dialog could not be opened (%v). Run the command again", err))
	}
	LogBasic("Live upgrade to %s asked for by %s: waiting for the person", bin.path, item.Name)
	timer := time.NewTimer(upgradeConfirmTimeout())
	defer timer.Stop()
	var out askOutcome
	select {
	case out = <-hold.done:
	case <-timer.C:
		out, _ = d.attention.closeAsk(hold, approvalEndTimeout)
	case <-d.ctx.Done():
		out, _ = d.attention.closeAsk(hold, askEndShutdown)
	}
	LogBasic("Live upgrade to %s: the ask ended %s %s", bin.path, out.Reason, out.Answer)
	switch {
	case out.Reason == AskAnswered && out.Answer == upgradeAnswerYes:
		return ""
	case out.Reason == AskAnswered:
		return "The person answered No. Do not ask again unless they ask for the upgrade"
	case out.Reason == approvalEndTimeout:
		return fmt.Sprintf("Nobody answered the dialog within %d seconds. Run the command again, and ask the person to choose Yes in the dialog", int(upgradeConfirmTimeout().Round(time.Second)/time.Second))
	case out.Reason == askEndShutdown:
		return "The daemon is shutting down"
	}
	return fmt.Sprintf("The dialog closed without an answer (%s). Run the command again, and ask the person to choose Yes in the dialog", out.Reason)
}

func (d *Daemon) upgradeDialogRefusal() string {
	if d.personClients() == 0 {
		return "No client is attached to show the dialog. Ask the person to attach a client, then run the command again"
	}
	if !d.agentsEnabled() {
		return "The agent features are off, so no client shows the dialog. Ask the person to run tuios daemon upgrade from a terminal outside tuios"
	}
	return ""
}

func (a *attentionStore) closeAsk(h *askHold, reason string) (askOutcome, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, open := a.asks[h.id]; open {
		h.waiting = false
		a.closeLocked(h.itemID, reason)
		return askOutcome{Reason: reason}, false
	}
	select {
	case out := <-h.done:
		return out, true
	default:
		return askOutcome{}, false
	}
}

func (a *attentionStore) askIsUpgrade(requestID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	h, ok := a.asks[requestID]
	if !ok {
		return false
	}
	it, ok := a.items[h.itemID]
	return ok && it.Upgrade != nil
}
