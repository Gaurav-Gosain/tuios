package app

import (
	"image/color"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// The upgrade dialog: a process inside a pane asked for a live upgrade of the
// daemon, and the daemon asks the person whether to go ahead
// (session/upgrade_confirm.go). The question is an ask in the Inbox mirror
// marked with Upgrade, and this client opens it as a dialog of its own.
//
// Only a key the person pressed here answers it. Keys send-keys or a tape put
// through the client answer nothing, a paste goes nowhere, and a click does
// nothing. A key in the first moment after it opens is dropped, since it was
// typed for whatever was in front of the person before. No is the default,
// and Esc is No.
//
// The dialog closes when the ask does: this client's answer, another
// client's, or the daemon giving up on it.

// overlayKindUpgradeConfirm is the dialog's overlay kind.
const overlayKindUpgradeConfirm = "upgradeconfirm"

// upgradeConfirmWidth is the dialog's preferred inner width.
const upgradeConfirmWidth = 66

// upgradeConfirmSettle is how long the dialog ignores keys after it opens.
const upgradeConfirmSettle = 500 * time.Millisecond

// upgradeConfirmRows are the answers, in drawn order: the daemon's options.
var upgradeConfirmRows = []string{"No", "Yes"}

// upgradeConfirmState is the ask the dialog shows.
type upgradeConfirmState struct {
	// open is the ask shown, nil when the dialog is closed.
	open *session.AttentionItem
	// selected is the answer row under the cursor.
	selected int
	// openedAt is when it opened. Keys are ignored for upgradeConfirmSettle.
	openedAt time.Time
	// sent is true once this client sent an answer.
	sent bool
}

// UpgradeConfirmOpen reports whether the dialog owns the keyboard.
func (m *OS) UpgradeConfirmOpen() bool { return m.upgradeConfirm.open != nil }

// syncUpgradeConfirm opens the dialog on an upgrade ask in the Inbox mirror,
// and closes it once its ask is gone. A client attached to another machine,
// or one the daemon issued no nonce, could not answer, so it shows nothing.
func (m *OS) syncUpgradeConfirm() {
	st := &m.upgradeConfirm
	if st.open != nil && m.inboxIndex(st.open.ID) < 0 {
		*st = upgradeConfirmState{}
	}
	if st.open != nil || m.AttachedHost != "" || m.DaemonClient == nil || m.DaemonClient.HumanNonce() == "" {
		return
	}
	for _, it := range m.Inbox.Items {
		if it.Upgrade != nil && it.Host == "" && it.RequestID != "" {
			*st = upgradeConfirmState{open: &it, openedAt: time.Now()}
			return
		}
	}
}

// UpgradeConfirmKey handles a key while the dialog is open.
func (m *OS) UpgradeConfirmKey(key string) tea.Cmd {
	st := &m.upgradeConfirm
	if st.open == nil || m.ProcessingRemoteKeys || m.pressingTapeKeys || time.Since(st.openedAt) < upgradeConfirmSettle {
		// Keys a script or a pane sent through the client (send-keys, a
		// tape's Press) are not the person's, and answer nothing.
		return nil
	}
	// Each key names its row rather than toggling, so no count of move keys
	// lands on Yes: Up then Enter, a shell's "run that again", stays on No.
	switch key {
	case "down", "right", "tab":
		st.selected = 1
	case "enter":
		return m.upgradeConfirmAnswer(st.selected)
	case "esc", "n":
		return m.upgradeConfirmAnswer(0)
	default:
		st.selected = 0
	}
	return nil
}

// upgradeConfirmAnswer sends the answer on row idx, with this client's attach
// nonce. The daemon takes the first answer from any client.
func (m *OS) upgradeConfirmAnswer(idx int) tea.Cmd {
	st := &m.upgradeConfirm
	if st.sent {
		return nil
	}
	st.sent, st.selected = true, idx
	return m.inboxAnswerAskCmd(*st.open, upgradeConfirmRows[idx], true)
}

// renderUpgradeConfirm draws the dialog. The binary's path is never cut: it
// wraps, since the path is what the person decides on.
func (m *OS) renderUpgradeConfirm() (string, overlay.Geometry, []overlayRowHit) {
	st := &m.upgradeConfirm
	it := st.open
	pal := m.overlayUI()
	bg := pal.Canvas
	width := overlay.DialogFitWidth(upgradeConfirmWidth, m.GetRenderWidth())
	inner := max(width-2, 1)

	var body []string
	text := func(s string, ink color.Color) {
		for _, l := range wrapPlain(s, inner) {
			body = append(body, dialogBodyLine(l, width, ink, bg))
		}
	}
	body = append(body, overlay.Fill(overlay.Style(bg).Render(" ")+
		overlay.Style(bg).Foreground(pal.Fg).Bold(true).Render(overlay.Truncate("Upgrade the daemon?", inner)), width, bg))
	body = append(body, dialogBodyLine("", width, pal.FgDim, bg))
	// The title is the pane's own, and says whatever the pane set, so the
	// pane id and session name the daemon placed it by stand beside it.
	who := inboxWho(*it)
	if it.Window != "" {
		who += " (pane " + shortID(it.Window) + " in session " + printableTitle(it.Session) + ")"
	}
	text(who+" asks to run the daemon as this binary, version "+it.Upgrade.Version+":", pal.Fg)
	for _, l := range breakEvery(it.Upgrade.Binary, inner-2) {
		body = append(body, dialogBodyLine("  "+l, width, pal.Warn, bg))
	}
	body = append(body, dialogBodyLine("", width, pal.FgDim, bg))
	text("Every pane keeps running. The binary gets every pane's terminal. Say yes only if you asked for it.", pal.FgDim)
	note := "Down or Tab moves to Yes, any other key back to No. Enter chooses."
	if st.sent {
		note = "Sending your answer..."
	}
	body = append(body, dialogBodyLine(note, width, pal.FgMute, bg))
	body = append(body, overlay.Fill(overlay.Style(bg).Render(" ")+overlay.DashRule(max(width-2, 0), bg, pal), width, bg))
	for i, label := range upgradeConfirmRows {
		body = append(body, dialogAnswerRow(label, st.selected == i, i == 1, width, pal))
	}
	hints := []overlay.Hint{{Key: overlay.EnterKey(), Label: "choose"}, {Key: "esc", Label: "no"}}
	content, geo := overlay.Dialog{Title: "live upgrade", Width: width, Body: strings.Join(body, "\n"), Hints: hints}.Render(pal)
	return content, geo, nil
}

// breakEvery cuts s into lines of at most width cells, keeping every
// character, spaces included.
func breakEvery(s string, width int) []string {
	width = max(width, 1)
	var out []string
	var line strings.Builder
	lineW := 0
	for s != "" {
		cluster, w := ansi.FirstGraphemeCluster(s, ansi.GraphemeWidth)
		if cluster == "" {
			break
		}
		if lineW+w > width && lineW > 0 {
			out = append(out, line.String())
			line.Reset()
			lineW = 0
		}
		line.WriteString(cluster)
		lineW += w
		s = s[len(cluster):]
	}
	return append(out, line.String())
}
