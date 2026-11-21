package app

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/integration"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

const overlayKindIntegrationAsk = "integrationask"

const integrationAskWidth = 56

const (
	integrationRowYes = iota
	integrationRowLater
	integrationRows
)

type integrationAsk struct {
	open     bool
	st       integration.Status
	verb     agentVerb
	paths    []string
	selected int
	openedAt time.Time
}

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
	if delta, ok := listDelta(m.ListKeys, key, integrationRows); ok {
		a.selected = min(max(a.selected+delta, 0), integrationRows-1)
		return nil
	}
	switch key {
	case "esc", "q", "n":
		m.CloseIntegrationAsk()
	case "shift+tab":
		a.selected = max(a.selected-1, 0)
	case "tab":
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

func (m *OS) renderIntegrationAsk() (string, overlay.Geometry, []overlayRowHit) {
	a := &m.integrationAsk
	pal := m.overlayUI()
	bg := pal.Canvas
	width := overlay.DialogFitWidth(integrationAskWidth, m.modalRoomWidth())
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
