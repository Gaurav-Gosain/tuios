package app

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

const overlayKindUpgradeConfirm = "upgradeconfirm"

const upgradeConfirmWidth = 66

const upgradeConfirmSettle = 500 * time.Millisecond

var upgradeConfirmRows = []string{"No", "Yes"}

type upgradeConfirmState struct {
	open     *session.AttentionItem
	selected int
	openedAt time.Time
	sent     bool
}

// UpgradeConfirmOpen reports whether the dialog owns the keyboard.
func (m *OS) UpgradeConfirmOpen() bool { return m.upgradeConfirm.open != nil }

func (m *OS) syncUpgradeConfirm() {
	st := &m.upgradeConfirm
	if st.open != nil && m.inboxIndex(st.open.ID) < 0 {
		*st = upgradeConfirmState{}
	}
	if st.open != nil || m.AttachedHost != "" || m.DaemonClient == nil || !m.DaemonClient.UpgradeDialog || m.DaemonClient.HumanNonce() == "" {
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
		return nil
	}
	switch key {
	case "down", "right":
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

func (m *OS) upgradeConfirmAnswer(idx int) tea.Cmd {
	st := &m.upgradeConfirm
	if st.sent {
		return nil
	}
	st.sent, st.selected = true, idx
	return m.inboxAnswerAskCmd(*st.open, upgradeConfirmRows[idx], true)
}

func (m *OS) renderUpgradeConfirm() (string, overlay.Geometry, []overlayRowHit) {
	st := &m.upgradeConfirm
	it := st.open
	pal := m.overlayUI()
	bg := pal.Canvas
	width := overlay.DialogFitWidth(upgradeConfirmWidth, m.modalRoomWidth())
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
	who := inboxWho(*it)
	if it.Window != "" {
		who += " (pane " + shortID(it.Window) + " in session " + printableTitle(it.Session) + ")"
	}
	text(who+" asks to run the daemon as this binary, version "+it.Upgrade.Version+":", pal.Fg)
	for _, l := range breakEvery(it.Upgrade.Binary, inner-2) {
		body = append(body, dialogBodyLine("  "+l, width, pal.Warn, bg))
	}
	body = append(body, dialogBodyLine("", width, pal.FgDim, bg))
	if n := len(it.Upgrade.CloseExited); n > 0 {
		text(fmt.Sprintf("It closes %d exited %s on other machines:", n, plural.Word(n, "pane", "panes")), pal.Fg)
		for _, label := range it.Upgrade.CloseExited {
			text("  "+printableTitle(label), pal.Warn)
		}
		body = append(body, dialogBodyLine("", width, pal.FgDim, bg))
	}
	text("Every pane keeps running. The binary gets every pane's terminal. Say yes only if you asked for it.", pal.FgDim)
	note := "Down moves to Yes, any other key back to No. Enter chooses."
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
