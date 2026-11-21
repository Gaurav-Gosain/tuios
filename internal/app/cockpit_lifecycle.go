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

type ckShip struct {
	card    session.TaskRow
	verb    string
	label   string
	confirm string
	lines   []string
	busy    bool
}

type ckShipMsg struct {
	sh          *ckShip
	verb, label string
	err         error
	confirm     string
	lines       []string
	url         string
}

func ckWorkPane(c session.TaskRow) (sess, win string, ok bool) {
	w := c.Work
	if c.Host != "" || w == nil || w.Host != "" || w.Session == "" || w.Window == "" {
		return "", "", false
	}
	return w.Session, w.Window, true
}

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
			m.ShowNotification(sh.label+" on "+sh.card.ID+" goes on. A notice says when it is done.", "info", m.Settings.NotificationDuration)
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
	sh.busy, sh.label = true, label
	call := m.inboxCaller()
	return func() tea.Msg {
		raw, err := call(verb, params, 3*time.Minute)
		msg := ckShipMsg{sh: sh, verb: verb, label: label, err: err}
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

func (m *OS) applyShip(msg ckShipMsg) {
	sh := msg.sh
	open := sh != nil && m.Inbox.ck.ship == sh
	defer m.cockpitTouch()
	what := msg.label
	if sh != nil {
		what += " on " + sh.card.ID
	}
	switch {
	case msg.err != nil:
		if open {
			m.Inbox.ck.ship = nil
		}
		m.ShowNotification(what+" did not go: "+printableTitle(msg.err.Error()), "error", m.Settings.NotificationDuration*2)
	case msg.confirm != "" && open:
		sh.busy = false
		sh.verb, sh.label, sh.confirm, sh.lines = msg.verb, msg.label, msg.confirm, msg.lines
	case msg.confirm != "":
		m.ShowNotification(what+" sent nothing. Open Ready to merge again to see what it sends.", "info", m.Settings.NotificationDuration*2)
	default:
		if open {
			m.Inbox.ck.ship = nil
		}
		text := what + " done."
		if msg.verb == "ship-pr" && msg.url != "" {
			text = "Opened " + printableTitle(msg.url)
		}
		m.ShowNotification(text, "success", m.Settings.NotificationDuration*2)
	}
}

func (m *OS) cockpitShipHints() []overlay.Hint {
	sh := m.Inbox.ck.ship
	switch {
	case sh.verb != "" && !sh.busy:
		what := strings.ToLower(sh.label) + " what the card shows"
		if len(sh.lines) > 0 {
			what = ckTrunc(printableTitle(sh.lines[0]), 72)
		}
		return []overlay.Hint{{Key: overlay.EnterKey(), Label: what, Priority: overlay.HintEssential}, {Key: "esc", Label: "cancel", Priority: overlay.HintEssential}}
	}
	return []overlay.Hint{{Key: "Ready to merge", Label: sh.card.ID}, {Key: "c", Label: "commit"}, {Key: "p", Label: "push"}, {Key: "o", Label: "open PR"}, {Key: "esc", Label: "close"}}
}
