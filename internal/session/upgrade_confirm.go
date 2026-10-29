package session

import (
	"os"
	"strconv"
	"time"
)

// A live upgrade asked for from inside a pane.
//
// The binary a live upgrade names runs as the daemon, which holds every
// pane's terminal and the key that signs every pane's token. So a process
// inside a pane does not get one by asking: the person has to say yes. But
// someone who works entirely inside tuios has no terminal outside it, so the
// daemon asks them where they are, in a dialog on every attached client.
//
// The dialog is an ask (ask_human.go) with the answers No and Yes, marked
// with the binary and its version (AttentionItem.Upgrade). That gives it the
// ask's proof that a person answered: answer-ask takes only the nonce of a
// client attached right now, from a process outside every pane
// (human_sender.go, human_origin.go), and never over a link. The client adds
// the other half: only a key the person pressed at that client answers it,
// never one send-keys or a tape put through the client, and never a paste
// (internal/app/upgrade_confirm.go). The first answer closes the ask, and the
// close takes the dialog off every other client.
//
// Anything but Yes refuses, with the refusal a pane always got: No, Esc, the
// wait running out, the ask being dismissed or superseded, and no client
// that could answer. A plugin is refused without a dialog, as is a call over
// a link, and a caller outside every pane needs none.

// upgradeConfirmWait is how long a pane's upgrade waits for the person.
const upgradeConfirmWait = 60 * time.Second

// UpgradeConfirmWait is upgradeConfirmWait, for the CLI's call timeout.
const UpgradeConfirmWait = upgradeConfirmWait

// The answers the dialog offers. No is first, so it is the default.
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
}

// upgradeConfirmTimeout is upgradeConfirmWait, or a test's shorter one.
func upgradeConfirmTimeout() time.Duration {
	if os.Getenv(handoffTestEnv) == "1" {
		if ms, err := strconv.Atoi(os.Getenv("TUIOS_TEST_UPGRADE_CONFIRM_MS")); err == nil && ms > 0 {
			return time.Duration(ms) * time.Millisecond
		}
	}
	return upgradeConfirmWait
}

// callerIsPlugin reports whether the process on cs is a plugin: it presented
// a plugin's token, or the kernel placed it in a plugin's process session.
func (d *Daemon) callerIsPlugin(cs *connState) bool {
	return cs != nil && (cs.pluginBound.Load() != nil || d.pluginOfPeer(cs) != nil)
}

// personClients is how many attached clients could answer for the person:
// local, with a process outside every pane.
func (d *Daemon) personClients() int {
	var attached []*connState
	d.clientsMu.RLock()
	for _, cs := range d.clients {
		cs.mu.Lock()
		if cs.attached && cs.isTUIClient && !cs.viaLink {
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

// confirmUpgrade asks the person whether the pane on cs may upgrade the
// daemon to bin, and reports whether they said yes. It returns false at once
// when no client could answer.
func (d *Daemon) confirmUpgrade(cs *connState, bin handoffBinary) bool {
	if d.personClients() == 0 {
		LogBasic("Live upgrade from a pane refused: no client is attached to ask")
		return false
	}
	if !d.agentsEnabled() {
		// The dialog rides the Inbox, which clients do not watch with the
		// agent features off.
		LogBasic("Live upgrade from a pane refused: the agent features are off, so no client shows the dialog")
		return false
	}
	if !askLinePrintable(bin.path) || !askLineShown(bin.accepts.Version, askMaxOption) {
		// What the dialog shows must be what the exec runs, character for
		// character.
		LogBasic("Live upgrade from a pane refused: the binary's path or version cannot be shown as it is")
		return false
	}
	item := AttentionItem{
		Name:    "a process inside a pane",
		Summary: "Upgrade the daemon to " + bin.accepts.Version + "?",
		Options: []string{upgradeAnswerNo, upgradeAnswerYes},
		Upgrade: &UpgradeAsk{Binary: bin.path, Version: bin.accepts.Version},
	}
	_, window := d.peerPane(cs)
	for _, sess := range d.manager.AllSessions() {
		if item.Session == "" {
			// A pane that cannot be placed still needs a session for its
			// item.
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
		return false
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
	return out.Reason == AskAnswered && out.Answer == upgradeAnswerYes
}

// closeAsk ends an open question that a waiting call gives up on, and takes
// it off every client, with reason. It reports how the question ended when
// an answer came first.
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

// askIsUpgrade reports whether the open question under requestID is a live
// upgrade's confirm.
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
