package app

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/integration"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// The question an integration notice opens. The notice's key is ctrl+b j, the
// key people press to jump to the newest message, so a press from habit must
// not write a harness's settings file. The notice opens this dialog, which
// names the file, and only its Install row writes it.
//
// It takes no answer for pluginApprovalSettle after it opens, as the plugin
// approval dialog does: an enter that was on its way as the dialog opened,
// or a second click, answers nothing.

// overlayKindIntegrationAsk is the dialog's overlay kind.
const overlayKindIntegrationAsk = "integrationask"

// integrationAskWidth is the dialog's preferred inner width.
const integrationAskWidth = 56

// The rows by index.
const (
	integrationRowYes = iota
	integrationRowLater
	integrationRows
)

// integrationAsk is the open question.
type integrationAsk struct {
	// open is true while the dialog is up.
	open bool
	// st is the harness the question is about, as the report said.
	st integration.Status
	// verb is agentInstall or agentUpdate.
	verb agentVerb
	// paths are the files the install writes.
	paths []string
	// selected is the row under the cursor.
	selected int
	// openedAt is when the dialog opened. See integrationAskSettled.
	openedAt time.Time
}

// openIntegrationAsk opens the question for st.
func (m *OS) openIntegrationAsk(st integration.Status, verb agentVerb) {
	paths := []string{st.Path}
	if t, ok := integration.LookupTarget(st.Harness); ok {
		paths = t.Paths(agentsEnv())
	}
	m.integrationAsk = integrationAsk{open: true, st: st, verb: verb, paths: paths, openedAt: pluginApprovalNow()}
}

// IntegrationAskOpen reports whether the dialog owns the keyboard.
func (m *OS) IntegrationAskOpen() bool { return m.integrationAsk.open }

// CloseIntegrationAsk closes the dialog. Nothing is written.
func (m *OS) CloseIntegrationAsk() { m.integrationAsk = integrationAsk{} }

// integrationAskSettled reports whether the dialog has been open long enough
// to take an answer.
func (m *OS) integrationAskSettled() bool {
	return pluginApprovalNow().Sub(m.integrationAsk.openedAt) >= pluginApprovalSettle
}

// IntegrationAskSelect puts the cursor on a row, as a hover does.
func (m *OS) IntegrationAskSelect(idx int) {
	if idx >= 0 && idx < integrationRows {
		m.integrationAsk.selected = idx
	}
}

// IntegrationAskKey handles a key while the dialog is open. Keys a script or a
// pane sent through the client answer nothing.
func (m *OS) IntegrationAskKey(key string) tea.Cmd {
	a := &m.integrationAsk
	if !a.open || !m.integrationAskSettled() || m.ProcessingRemoteKeys || m.pressingTapeKeys {
		return nil
	}
	switch key {
	case "esc", "q", "n":
		m.CloseIntegrationAsk()
	case "up", "k", "shift+tab":
		a.selected = max(a.selected-1, 0)
	case "down", "j", "tab":
		a.selected = min(a.selected+1, integrationRows-1)
	case "enter":
		return m.IntegrationAskActivate(a.selected)
	}
	return nil
}

// IntegrationAskActivate answers with row idx, from enter or a click.
func (m *OS) IntegrationAskActivate(idx int) tea.Cmd {
	a := m.integrationAsk
	if !a.open || !m.integrationAskSettled() {
		return nil
	}
	m.CloseIntegrationAsk()
	if idx != integrationRowYes {
		return nil
	}
	if m.agentsPage.busy {
		m.ShowNotification("Another change is running. Wait for it to end.", "info", m.Settings.NotificationDuration)
		return nil
	}
	return m.runAgentAction(a.st, a.verb)
}

// renderIntegrationAsk draws the dialog: the question, the file, the rows.
func (m *OS) renderIntegrationAsk() (string, overlay.Geometry, []overlayRowHit) {
	a := &m.integrationAsk
	pal := m.overlayUI()
	bg := pal.Canvas
	width := overlay.DialogFitWidth(integrationAskWidth, m.GetRenderWidth())
	word, question, effect := "Install", "Install the "+a.st.Name+" integration?", "This adds hooks to "+joinPaths(a.paths)+"."
	if a.verb == agentUpdate {
		word, question, effect = "Update", "Update the "+a.st.Name+" integration?", "This updates the hooks in "+joinPaths(a.paths)+"."
	}
	body := []string{
		overlay.Fill(overlay.Style(bg).Render(" ")+
			overlay.Style(bg).Foreground(pal.Fg).Bold(true).Render(overlay.Truncate(question, max(width-1, 1))), width, bg),
	}
	for _, l := range wrapPlain(effect, max(width-2, 10)) {
		body = append(body, dialogBodyLine(l, width, pal.FgDim, bg))
	}
	body = append(body, overlay.Fill(overlay.Style(bg).Render(" ")+overlay.DashRule(max(width-2, 0), bg, pal), width, bg))
	top := len(body)
	body = append(body,
		dialogAnswerRow(word, a.selected == integrationRowYes, false, width, pal),
		dialogAnswerRow("Not now", a.selected == integrationRowLater, false, width, pal))
	content, geo := overlay.Dialog{
		Title: "agent integration",
		Width: width,
		Body:  strings.Join(body, "\n"),
		Hints: []overlay.Hint{{Key: overlay.EnterKey(), Label: "choose"}, {Key: "esc", Label: "not now"}},
	}.Render(pal)
	hits := make([]overlayRowHit, 0, integrationRows)
	for i := range integrationRows {
		y := geo.BodyY + top + i
		hits = append(hits, overlayRowHit{Rect: overlay.Rect{X0: 0, Y0: y, X1: geo.Width, Y1: y + 1}, Idx: i})
	}
	return content, geo, hits
}
