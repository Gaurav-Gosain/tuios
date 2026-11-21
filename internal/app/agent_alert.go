package app

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/sound"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

type pendingAgentAlert struct {
	windowID string
	from     string
	to       string
	due      time.Time
}

func (m *OS) agentAlertPolicy() config.AgentAlertPolicy {
	if m.UserConfig == nil {
		return config.ResolveAgentAlerts(nil)
	}
	return config.ResolveAgentAlerts(&m.UserConfig.Notifications.Agent)
}

func agentAlertQuiet(w *terminal.Window, to string) bool {
	return quietHoldsAlert(w.AgentQuiet, w.AgentKind, to)
}

func quietHoldsAlert(quiet bool, kind, to string) bool {
	return quiet && (to != "needs_input" || !session.AgentKindReachesPerson(kind))
}

func (m *OS) noteQuietLifted(w *terminal.Window, fromState string, fromQuiet bool, fromKind string) {
	if fromState != "needs_input" || w.AgentState != "needs_input" {
		return
	}
	if quietHoldsAlert(fromQuiet, fromKind, "needs_input") && !agentAlertQuiet(w, "needs_input") {
		m.considerAgentAlert(w, "needs_input", "needs_input")
	}
}

func (m *OS) considerAgentAlert(w *terminal.Window, from, to string) {
	if !m.agentsOn() {
		return
	}
	m.NotifyDockEvent(string(hooks.AfterAgentState))
	if w == nil {
		return
	}
	policy := m.agentAlertPolicy()

	delete(m.pendingAgentAlerts, w.ID)

	m.agentAlertRuns.Baseline(w.ID, from, w.AgentCompletionSeq)
	if !m.agentAlertRuns.Due(w.ID, to, w.AgentCompletionSeq) {
		return
	}

	if agentAlertQuiet(w, to) {
		return
	}
	if to == "needs_input" && m.paneWithParent(w.ID) {
		return
	}
	if !policy.Alerts(to) {
		return
	}
	if policy.SuppressFocused && m.GetFocusedWindow() == w && m.HostLooking() {
		return
	}
	if policy.Quiet(time.Now()) {
		return
	}
	if policy.Settle <= 0 {
		m.fireAgentAlert(w, from, to, policy)
		return
	}
	if m.pendingAgentAlerts == nil {
		m.pendingAgentAlerts = make(map[string]pendingAgentAlert, 1)
	}
	m.pendingAgentAlerts[w.ID] = pendingAgentAlert{
		windowID: w.ID,
		from:     from,
		to:       to,
		due:      time.Now().Add(policy.Settle),
	}
}

func (m *OS) flushDueAgentAlerts(now time.Time) {
	if len(m.pendingAgentAlerts) == 0 {
		return
	}
	policy := m.agentAlertPolicy()
	for id, p := range m.pendingAgentAlerts {
		if now.Before(p.due) {
			continue
		}
		delete(m.pendingAgentAlerts, id)
		w := m.windowByID(id)
		if w == nil || w.AgentState != p.to {
			continue
		}
		if policy.SuppressFocused && m.GetFocusedWindow() == w && m.HostLooking() {
			continue
		}
		m.fireAgentAlert(w, p.from, p.to, policy)
	}
}

//go:noinline
func (m *OS) windowByID(id string) *terminal.Window {
	for _, w := range m.Windows {
		if w != nil && w.ID == id {
			return w
		}
	}
	return nil
}

func (m *OS) fireAgentAlert(w *terminal.Window, from, to string, policy config.AgentAlertPolicy) {
	if agentAlertQuiet(w, to) || to == "needs_input" && m.paneWithParent(w.ID) {
		return
	}
	word, sev := agentTransitionNotice(to)
	if word == "" {
		return
	}
	if !m.agentAlertRuns.Due(w.ID, to, w.AgentCompletionSeq) {
		return
	}
	m.agentAlertRuns.Note(w.ID, to, w.AgentCompletionSeq)
	if n := m.agentAlertRuns.Len(); n > 2*len(m.Windows)+16 {
		m.agentAlertRuns.Keep(func(id string) bool { return m.windowByID(id) != nil })
	}
	name := m.agentAlertName(w)
	text := name + " " + word
	if note := printableTitle(w.AgentMessage); note != "" {
		text += agentAlertSep() + note
	}

	if policy.Dock {
		m.showAgentNotification(text, sev, to, m.Settings.NotificationDuration,
			NotifTarget{SessionID: m.sidebarCurrentSessionID(), WindowID: w.ID})
	}
	if m.Settings.Calm && !policy.AttentionCue(to) {
		return
	}
	if m.programSourced(w) {
		m.programAlertOutside(w, from, to, name, text, policy, time.Now())
		return
	}
	m.fireAgentAlertOutside(w, from, to, name, text, policy)
}

func (m *OS) fireAgentAlertOutside(w *terminal.Window, from, to, name, text string, policy config.AgentAlertPolicy) {
	var seq []byte
	if policy.Notify && !m.BrowserClient {
		seq = hostNotifySequence(text, m.detectOuterMultiplexer())
	}
	if policy.PlaysBell() {
		seq = append(seq, 0x07)
	}
	m.writeHostSequence(seq)

	if policy.PlaysAudio() {
		cue := sound.CueDone
		if policy.AttentionCue(to) {
			cue = sound.CueAttention
		}
		sound.Play(sound.Request{Cue: cue, File: policy.CueFile(to), Cooldown: policy.SoundCooldown})
	}

	m.FireHookContext(hooks.AfterAgentState, hooks.Context{
		WindowID:       w.ID,
		WindowName:     name,
		AgentState:     to,
		PrevAgentState: from,
		AgentHarness:   w.AgentHarness,
		AgentMessage:   w.AgentMessage,
	})
}

const programAlertGap = 30 * time.Second

type programAlert struct {
	at      time.Time
	to      string
	pending *pendingAgentAlert
}

func (m *OS) programSourced(w *terminal.Window) bool {
	sum, ok := programSummary(w.ProgramStatus)
	if !ok {
		return false
	}
	return programAgentState(sum.State) == w.AgentState && programRecordMessage(sum) == w.AgentMessage
}

func programAgentState(state string) string {
	switch state {
	case "blocked":
		return "needs_input"
	case "error":
		return "errored"
	}
	return state
}

func programRecordMessage(r sessiontree.ProgramRecord) string {
	switch {
	case r.Title != "" && r.Msg != "":
		return r.Title + ": " + r.Msg
	case r.Msg != "":
		return r.Msg
	}
	return r.Title
}

func (m *OS) programAlertOutside(w *terminal.Window, from, to, name, text string, policy config.AgentAlertPolicy, now time.Time) {
	if m.programAlerts == nil {
		m.programAlerts = make(map[string]*programAlert)
	}
	m.pruneProgramAlerts()
	pa := m.programAlerts[w.ID]
	switch {
	case pa == nil || now.Sub(pa.at) >= programAlertGap:
		m.programAlerts[w.ID] = &programAlert{at: now, to: to}
		m.fireAgentAlertOutside(w, from, to, name, text, policy)
	case to == pa.to:
		pa.pending = nil
	default:
		pa.pending = &pendingAgentAlert{windowID: w.ID, from: from, to: to, due: pa.at.Add(programAlertGap)}
	}
}

func (m *OS) flushProgramAlerts(now time.Time) {
	if !m.programAlertHeld() {
		return
	}
	policy := m.agentAlertPolicy()
	for id, pa := range m.programAlerts {
		p := pa.pending
		if p == nil || now.Before(p.due) {
			continue
		}
		pa.pending = nil
		w := m.windowByID(id)
		if w == nil || w.AgentState != p.to || !policy.Alerts(p.to) || policy.Quiet(now) {
			continue
		}
		word, _ := agentTransitionNotice(p.to)
		if word == "" {
			continue
		}
		name := m.agentAlertName(w)
		text := name + " " + word
		if note := printableTitle(w.AgentMessage); note != "" {
			text += agentAlertSep() + note
		}
		pa.at, pa.to = now, p.to
		m.fireAgentAlertOutside(w, p.from, p.to, name, text, policy)
	}
}

func (m *OS) programAlertHeld() bool {
	for _, pa := range m.programAlerts {
		if pa.pending != nil {
			return true
		}
	}
	return false
}

func (m *OS) pruneProgramAlerts() {
	for id := range m.programAlerts {
		if m.windowByID(id) == nil {
			delete(m.programAlerts, id)
		}
	}
}

func (m *OS) agentAlertName(w *terminal.Window) string {
	name := printableTitle(m.railTitleShown(w))
	if name == "" {
		name = "pane"
	}
	if len(w.ProgramStatus) > 0 {
		name += " [" + shortWindowLabel(w.ID) + "]"
	}
	return name
}

func agentAlertSep() string {
	if overlay.UseASCII() {
		return " - "
	}
	return " · "
}
