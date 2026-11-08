package app

import (
	"errors"
	"image/color"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The card lifecycle on the board. A card its agent is done with says how its
// verify went (Verifying, Verify passed, Attempt N failed and why). v opens
// the Changes view of the worktree the agent did the work in, while that pane
// is open. M on a card in review is Ready to merge: c commits the work with
// the card's title, p pushes the branch, o pushes it and opens the pull
// request. They are the ship verbs, called from this client as the person; a
// push and a pull request answer with what they would send first, and enter
// sends it.

// ckShip is the open Ready to merge choice: the card, and once a push or a
// pull request said what it sends, the verb that waits for enter.
type ckShip struct {
	card    session.TaskRow
	verb    string
	label   string
	confirm string
	lines   []string
	busy    bool
}

// ckShipMsg is a ship verb's answer.
type ckShipMsg struct {
	verb, label string
	err         error
	confirm     string
	lines       []string
	url         string
}

// ckWorkPane is the pane here that did the card's work.
func ckWorkPane(c session.TaskRow) (sess, win string, ok bool) {
	w := c.Work
	if c.Host != "" || w == nil || w.Host != "" || w.Session == "" || w.Window == "" {
		return "", "", false
	}
	return w.Session, w.Window, true
}

// ckLifeLine is what a free card's second line says of its lifecycle, and
// in which ink; "" for a card with nothing to say.
func ckLifeLine(pal overlay.Palette, c session.TaskRow) (string, color.Color) {
	v := c.Verify
	switch {
	case c.Stage == session.TaskStageVerify && v != nil && v.State == session.VerifyRunning:
		return "Verifying", pal.Accent
	case c.Stage == session.TaskStageVerify && v != nil && v.State == session.VerifyFailed:
		return "Verify did not run: " + printableTitle(v.Note), pal.Warn
	case c.Stage == session.TaskStageReview && v != nil && v.State == session.VerifyPassed:
		return "Verify passed", pal.Success
	case c.Stage == session.TaskStageReview:
		return "Ready for review", pal.FgDim
	case c.Attempts > 0 && (c.Stage == session.TaskStageTodo || c.Stage == session.TaskStagePlan):
		return "Attempt " + strconv.Itoa(c.Attempts) + " failed: " + printableTitle(c.Note), pal.Warn
	}
	return "", nil
}

// ckLifeFocus are the Card tab's lines on the lifecycle: the row's line,
// and in review what Ready to merge does or sends.
func (m *OS) ckLifeFocus(pal overlay.Palette, c session.TaskRow) [][]ckSeg {
	var out [][]ckSeg
	if words, ink := ckLifeLine(pal, c); words != "" {
		out = append(out, []ckSeg{{t: words, fg: ink, attrs: ckBold}})
	}
	if sh := m.Inbox.ck.ship; sh != nil && sh.verb != "" && ckTaskKey(sh.card) == ckTaskKey(c) {
		out = append(out, []ckSeg{{t: sh.label + " sends", fg: pal.Fg, attrs: ckBold}})
		for _, l := range sh.lines {
			out = append(out, []ckSeg{{t: "  " + printableTitle(l), fg: pal.FgDim}})
		}
	}
	return out
}

// ckLifeHints is M on a card in review whose work is here. v, the
// changes, is on the key bar already.
func (m *OS) ckLifeHints(c session.TaskRow) []overlay.Hint {
	if _, _, ok := ckWorkPane(c); !ok || c.Holder != nil || c.Stage != session.TaskStageReview {
		return nil
	}
	return m.keyHints(config.ActionInboxShip, "ready to merge")
}

// CockpitCardReview is v on a card: the Changes view of its work pane.
func (m *OS) CockpitCardReview() (tea.Cmd, bool) {
	_, c, ok := m.cockpitCard()
	sess, win, here := ckWorkPane(c)
	if !ok || !here || c.Holder != nil {
		return nil, false
	}
	return m.openReview(sess, win, printableTitle(c.Title)), true
}

// CockpitShipOpen reports whether Ready to merge is open.
func (m *OS) CockpitShipOpen() bool { return m.Inbox.ck.ship != nil }

// CockpitShip is M: Ready to merge on the card in review under the cursor.
func (m *OS) CockpitShip() tea.Cmd {
	_, c, ok := m.cockpitCard()
	if !ok {
		return nil
	}
	if _, _, here := ckWorkPane(c); !here || c.Stage != session.TaskStageReview {
		m.ShowNotification("Ready to merge is for a card in review, with its work on this machine.", "info", m.Settings.NotificationDuration)
		return nil
	}
	if _, ok := m.signedNonce(); !ok {
		return nil
	}
	m.Inbox.ck.ship = &ckShip{card: c}
	m.cockpitTouch()
	return nil
}

// CockpitShipKey takes the key while Ready to merge is open: c, p or o
// starts one, enter sends what a push or a pull request said it sends, and
// any other key closes it.
func (m *OS) CockpitShipKey(key string) tea.Cmd {
	sh := m.Inbox.ck.ship
	if sh == nil {
		return nil
	}
	defer m.cockpitTouch()
	if sh.busy {
		if key == "esc" {
			m.Inbox.ck.ship = nil
		}
		return nil
	}
	switch {
	case sh.verb != "" && key == "enter":
		return m.shipCall(sh.verb, sh.label, sh.confirm)
	case sh.verb == "" && key == "c":
		return m.shipCall("ship-commit", "Commit", "")
	case sh.verb == "" && key == "p":
		return m.shipCall("ship-push", "Push", "")
	case sh.verb == "" && key == "o":
		return m.shipCall("ship-pr", "Open PR", "")
	}
	m.Inbox.ck.ship = nil
	return nil
}

// shipCall runs one ship verb on the card's work pane, with confirm when the
// person saw what it sends.
func (m *OS) shipCall(verb, label, confirm string) tea.Cmd {
	sh := m.Inbox.ck.ship
	if _, ok := m.signedNonce(); !ok {
		m.Inbox.ck.ship = nil
		return nil
	}
	sess, win, _ := ckWorkPane(sh.card)
	params := map[string]any{"session": sess, "window": win}
	switch verb {
	case "ship-commit":
		params["message"] = sh.card.Title
	case "ship-pr":
		params["title"] = sh.card.Title
	}
	if confirm != "" {
		params["confirm"] = confirm
	}
	sh.busy = true
	call := m.inboxCaller()
	return func() tea.Msg {
		raw, err := call(verb, params, 3*time.Minute)
		msg := ckShipMsg{verb: verb, label: label, err: err}
		if verr, ok := errors.AsType[*session.VerbCallError](err); ok && verr.Code == session.ErrVerbConfirmRequired && verr.Hint != nil && confirm == "" {
			msg.err, msg.confirm, msg.lines = nil, verr.Hint.Confirm, verr.Hint.Available
		}
		if err == nil {
			var out struct{ URL string }
			_ = jsonx.Unmarshal(raw, &out)
			msg.url = out.URL
		}
		return msg
	}
}

// applyShip takes a ship verb's answer.
func (m *OS) applyShip(msg ckShipMsg) {
	sh := m.Inbox.ck.ship
	if sh == nil {
		return
	}
	defer m.cockpitTouch()
	sh.busy = false
	switch {
	case msg.err != nil:
		m.Inbox.ck.ship = nil
		m.ShowNotification(msg.label+" did not go: "+printableTitle(msg.err.Error()), "error", m.Settings.NotificationDuration*2)
	case msg.confirm != "":
		sh.verb, sh.label, sh.confirm, sh.lines = msg.verb, msg.label, msg.confirm, msg.lines
	default:
		m.Inbox.ck.ship = nil
		text := msg.label + " done."
		if msg.verb == "ship-pr" && msg.url != "" {
			text = "Opened " + printableTitle(msg.url)
		}
		m.ShowNotification(text, "success", m.Settings.NotificationDuration*2)
	}
}

// cockpitShipHints are Ready to merge's keys for the key bar.
func (m *OS) cockpitShipHints() []overlay.Hint {
	sh := m.Inbox.ck.ship
	switch {
	case sh.verb != "" && !sh.busy:
		// The columns have no Card tab: the key bar says what goes.
		what := strings.ToLower(sh.label) + " what the card shows"
		if len(sh.lines) > 0 {
			what = ckTrunc(printableTitle(sh.lines[0]), 72)
		}
		return []overlay.Hint{{Key: overlay.EnterKey(), Label: what, Priority: overlay.HintEssential}, {Key: "esc", Label: "cancel", Priority: overlay.HintEssential}}
	}
	return []overlay.Hint{{Key: "Ready to merge", Label: sh.card.ID}, {Key: "c", Label: "commit"}, {Key: "p", Label: "push"}, {Key: "o", Label: "open PR"}, {Key: "esc", Label: "close"}}
}
