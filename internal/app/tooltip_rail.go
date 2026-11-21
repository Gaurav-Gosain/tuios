package app

import (
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/federation"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

func (m *OS) sidebarTooltipTrack(x, y int) {
	for _, r := range m.sidebarStripRows {
		if r.contains(y) {
			m.tooltipTrack(tooltipRailStrip, y)
			return
		}
	}
	if h, ok := m.sidebarRowAt(x, y); ok && sidebarAddKind(h.Kind) {
		m.tooltipTrack(tooltipRailAdd, int(h.Kind))
		return
	}
	if h, ok := m.sidebarRowAt(x, y); ok && (h.Kind == sidebarRowHost || h.Kind == sidebarRowHostSignIn) && m.hostWaitsForSignIn(h.SessionID) {
		m.tooltipTrack(tooltipRailHost, h.Y0)
		return
	}
	m.tooltipClear()
}

func sidebarAddKind(k sidebarRowKind) bool {
	return k == sidebarRowNewSession || k == sidebarRowNewWindow
}

func sidebarAddWords(k sidebarRowKind) string {
	if k == sidebarRowNewWindow {
		return "new terminal"
	}
	return "new session"
}

func (m *OS) renderRailAddTooltip() *lipgloss.Layer {
	if !m.tooltipVisible(tooltipRailAdd) {
		return nil
	}
	m.Tooltip.Shown = true

	kind := sidebarRowKind(m.Tooltip.Key)
	row := -1
	for _, h := range m.SidebarHits {
		if h.Kind == kind && sidebarAddKind(h.Kind) {
			row = h.Y0
			break
		}
	}
	if row < 0 {
		return nil
	}

	railW, renderW := m.GetSidebarWidth(), m.GetRenderWidth()
	label := tooltipLabel(sidebarAddWords(kind), max(renderW-railW-1, 1), theme.UI())

	return m.railTooltipLayer(label, row)
}

const hostSignInTooltip = federation.SignInSentence

func (m *OS) renderRailHostTooltip() *lipgloss.Layer {
	if !m.tooltipVisible(tooltipRailHost) {
		return nil
	}
	m.Tooltip.Shown = true
	row := -1
	for _, h := range m.SidebarHits {
		if h.Kind == sidebarRowHost && h.Y0 == m.Tooltip.Key && m.hostWaitsForSignIn(h.SessionID) {
			row = h.Y0
			break
		}
	}
	if row < 0 {
		return nil
	}
	railW, renderW := m.GetSidebarWidth(), m.GetRenderWidth()
	label := tooltipLabel(hostSignInTooltip, max(renderW-railW-1, 1), theme.UI())
	return m.railTooltipLayer(label, row)
}

func sidebarTooltipBadgeLabel(info sidebarStripBadgeInfo) string {
	if info.Count == 0 {
		return ""
	}
	words := sidebarStateWords(info.State)
	if info.State == "needs_input" {
		words = agentNeedsYou(info.Count)
	}
	return plural.Count(info.Count, "agent") + " " + words
}

func sidebarTooltipSessionLabel(s sessiontree.Node) string {
	sep := " · "
	if overlay.UseASCII() {
		sep = " - "
	}
	label := printableTitle(s.Title) + sep + plural.Count(s.WindowCount, "terminal")
	if sidebarAttention(s.AgentState) {
		loud := agentStateIndicator(s.AgentState) + " " + sidebarStateWords(s.AgentState)
		if age := agentElapsed(s.AgentState, s.StateAt, time.Now()); age != "" {
			loud += " " + age
		}
		label += "  " + loud
	}
	return label
}

func sidebarTooltipTerminalLabel(e sidebarTerminalEntry) string {
	sep := " · "
	if overlay.UseASCII() {
		sep = " - "
	}
	label := printableTitle(e.Title)
	if label == "" {
		label = "shell"
	}
	if e.Tag != "" {
		label += sep + e.Tag
	}
	if e.State != "" {
		label += sep + sidebarStateWords(e.State)
	}
	return label
}

func sidebarTooltipAgentLabel(e sidebarAgentEntry) string {
	sep := " · "
	if overlay.UseASCII() {
		sep = " - "
	}
	bare := printableTitle(e.Title)
	if bare == "" {
		bare = "shell"
	}
	name := bare
	if e.Foreign {
		if s := printableTitle(e.SessionLabel); s != "" {
			name = s + "/" + name
		}
	}
	label := name
	if h := sidebarHarnessLabel(e.Harness); h != "" && !strings.EqualFold(h, bare) {
		label += sep + h
	}
	label += sep + sidebarStateWords(e.State)
	if age := agentElapsed(e.State, e.StateAt, time.Now()); age != "" {
		label += " " + age
	}
	if e.Quiet {
		label += sep + "quiet"
	}
	return label
}

func sidebarStateWords(state string) string {
	switch state {
	case "needs_input":
		return "needs you"
	case "errored":
		return "errored"
	case "working":
		return "working"
	case "done":
		return "done"
	case "unknown":
		return "unknown"
	case "limit_reached":
		return "limit reached"
	case string(agentStateWaiting):
		return "waiting"
	default:
		return "idle"
	}
}

func agentNeedsYou(n int) string {
	if n == 1 {
		return "needs you"
	}
	return "need you"
}

func (m *OS) renderRailTooltip() *lipgloss.Layer {
	if !m.tooltipVisible(tooltipRailStrip) {
		return nil
	}
	m.Tooltip.Shown = true

	text, row := "", m.Tooltip.Key
	for _, r := range m.sidebarStripRows {
		if r.contains(m.Tooltip.Key) {
			text, row = r.Label, r.Y0
			break
		}
	}
	if text == "" {
		return nil
	}

	railW, renderW := m.GetSidebarWidth(), m.GetRenderWidth()
	label := tooltipLabel(text, max(renderW-railW-1, 1), theme.UI())

	return m.railTooltipLayer(label, row)
}
