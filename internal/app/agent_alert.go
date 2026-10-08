package app

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/sessiontree"
	"github.com/Gaurav-Gosain/tuios/internal/sound"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// Agent alerts run on the client, not the daemon, and that is the design rather
// than an accident.
//
// The daemon owns agent state and is where every transition becomes
// authoritative, but three things live only on the client: the terminal an
// in-band notification has to reach, the user config (the daemon reads three
// keys at startup and the in-process daemon under `tuios ssh` reads none), and
// the dock stack whose messages are already clickable. So the client alerts on
// the authoritative transitions it observes through the state sync, which is
// every transition the daemon published.
//
// The consequence is stated rather than hidden: a session with nobody attached
// raises nothing. There is no terminal to write to and no dock to draw on, and
// the escape hatch that would work anyway (a command reaching a phone) is the
// one thing that would need a daemon-side copy of all of the above.

// pendingAgentAlert is an alert waiting out the settle window. Holding the
// window id rather than the pointer means a pane closed during the wait simply
// fails the lookup instead of resurrecting a dead window.
type pendingAgentAlert struct {
	windowID string
	from     string
	to       string
	due      time.Time
}

// agentAlertPolicy resolves the current [notifications.agent] policy.
//
// It resolves per call rather than being cached on the model: transitions are
// rare (a handful a minute at the very most), the resolve is a few field reads
// and one small map, and doing it here means a config reload is picked up with
// no extra wiring and a zero-value OS in a test gets the documented defaults
// rather than "everything off".
func (m *OS) agentAlertPolicy() config.AgentAlertPolicy {
	if m.UserConfig == nil {
		return config.ResolveAgentAlerts(nil)
	}
	return config.ResolveAgentAlerts(&m.UserConfig.Notifications.Agent)
}

// considerAgentAlert decides what one transition earns. It runs on the Update
// goroutine inside the state sync, so it does no I/O beyond the host write,
// which is a single mutex-guarded Write.
func (m *OS) considerAgentAlert(w *terminal.Window, from, to string) {
	if !m.agentsOn() {
		return
	}
	if w == nil {
		return
	}
	policy := m.agentAlertPolicy()

	// Any further transition retires whatever was parked for this pane: the
	// state it was going to announce is no longer the state the pane is in. This
	// is the whole anti-flicker rule, and it is why a pane that flips out and
	// back inside the settle window produces nothing rather than two alerts.
	delete(m.pendingAgentAlerts, w.ID)

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

// flushDueAgentAlerts raises the parked alerts whose settle window has expired
// and whose pane is still in the state they were parked for. It is called from
// the maintenance tick, which the idle gate keeps awake only while something is
// parked.
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
		// Re-validate rather than trust the parked state: the pane may have
		// closed, moved on, or been focused (and so read) while it waited.
		if w == nil || w.AgentState != p.to {
			continue
		}
		if policy.SuppressFocused && m.GetFocusedWindow() == w && m.HostLooking() {
			continue
		}
		m.fireAgentAlert(w, p.from, p.to, policy)
	}
}

// windowByID finds a live window by id, or nil.
func (m *OS) windowByID(id string) *terminal.Window {
	for _, w := range m.Windows {
		if w != nil && w.ID == id {
			return w
		}
	}
	return nil
}

// fireAgentAlert writes the alert to every sink the policy leaves on.
func (m *OS) fireAgentAlert(w *terminal.Window, from, to string, policy config.AgentAlertPolicy) {
	word, sev := agentTransitionNotice(to)
	if word == "" {
		return
	}
	name := m.agentAlertName(w)
	text := name + " " + word
	// The reason, when the pane gave one: the question a blocked agent asked,
	// or the note a report carried. Without it the alert says that the agent
	// needs somebody and not what it wants, and the person has to go and look.
	if note := printableTitle(w.AgentMessage); note != "" {
		text += agentAlertSep() + note
	}

	if policy.Dock {
		m.showAgentNotification(text, sev, to, m.Settings.NotificationDuration,
			NotifTarget{SessionID: m.sidebarCurrentSessionID(), WindowID: w.ID})
	}
	// What reaches outside tuios is rate limited for a pane whose state
	// comes from its OSC 7501 report: a program can change state as fast as
	// it writes. See programAlertOutside.
	if m.programSourced(w) {
		m.programAlertOutside(w, from, to, name, text, policy, time.Now())
		return
	}
	m.fireAgentAlertOutside(w, from, to, name, text, policy)
}

// fireAgentAlertOutside writes the parts of an alert that leave tuios: the
// terminal notification, the bell, the sound and the client hook.
func (m *OS) fireAgentAlertOutside(w *terminal.Window, from, to, name, text string, policy config.AgentAlertPolicy) {
	// One write for both, so a terminal that treats BEL as "raise the window"
	// does not race the notification it belongs to.
	var seq []byte
	// A browser terminal parses OSC 9 and drops it, so writing it there buys
	// nothing and the warning at startup already said so (browser_client.go).
	if policy.Notify && !m.BrowserClient {
		seq = hostNotifySequence(text, m.detectOuterMultiplexer())
	}
	if policy.PlaysBell() {
		seq = append(seq, 0x07)
	}
	m.writeHostSequence(seq)

	// The cue plays from the client process, not the daemon, so a local attach
	// plays it where the human sits. A served client is the exception: under
	// `tuios ssh` this code runs on the server, the audio comes out of the
	// server's speakers, and the startup notice (sshAlertNotices) already
	// said so. Play returns before anything is spawned, so the Update
	// goroutine this runs on is not waiting on an audio device.
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

// programAlertGap is the shortest time between two alerts outside tuios
// from one pane whose state comes from OSC 7501. The dock still shows each.
const programAlertGap = 30 * time.Second

// programAlert is the alert record of one such pane: when an alert last went
// outside tuios and for which state, and the alert held back since, if any.
type programAlert struct {
	at      time.Time
	to      string
	pending *pendingAgentAlert
}

// programSourced reports whether the pane's agent state is its own OSC 7501
// report, rather than a hook's or a rule's: the pane holds records, and the
// state and message are the ones its summary record maps to. A hook that
// reports for the same pane outranks the program, and its alerts are not
// limited here.
func (m *OS) programSourced(w *terminal.Window) bool {
	sum, ok := programSummary(w.ProgramStatus)
	if !ok {
		return false
	}
	return programAgentState(sum.State) == w.AgentState && programRecordMessage(sum) == w.AgentMessage
}

// programAgentState is the agent state a record's state maps to, as the
// daemon maps it.
func programAgentState(state string) string {
	switch state {
	case "blocked":
		return "needs_input"
	case "error":
		return "errored"
	}
	return state
}

// programRecordMessage is the message the daemon makes of a record: title and
// msg joined, as session.ProgramStatusMessage does.
func programRecordMessage(r sessiontree.ProgramRecord) string {
	switch {
	case r.Title != "" && r.Msg != "":
		return r.Title + ": " + r.Msg
	case r.Msg != "":
		return r.Msg
	}
	return r.Title
}

// programAlertOutside sends an OSC 7501 pane's alert outside tuios at most
// once in programAlertGap. Within the gap, an alert for the state last sent
// is a repeat and is dropped; an alert for another state is held, and the
// newest one held goes out when the gap ends, if the pane is still in that
// state (flushProgramAlerts). So a program that flips between two states
// costs one notification in 30 seconds, and a real change is never lost.
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

// flushProgramAlerts sends the held alerts whose gap has ended and whose pane
// is still in the state they were held for. It runs from the maintenance
// tick, which tickNeedsWork keeps awake while one is held.
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

// programAlertHeld reports whether an alert is held back.
func (m *OS) programAlertHeld() bool {
	for _, pa := range m.programAlerts {
		if pa.pending != nil {
			return true
		}
	}
	return false
}

// pruneProgramAlerts forgets the panes that have closed.
func (m *OS) pruneProgramAlerts() {
	for id := range m.programAlerts {
		if m.windowByID(id) == nil {
			delete(m.programAlerts, id)
		}
	}
}

// agentAlertName is how an alert names a pane: its title, and for a pane
// that reports over OSC 7501, which can set its own title, its id as well,
// which no program can set.
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

// agentAlertSep joins an alert's headline to the reason behind it, in the
// separator the rail uses everywhere else.
func agentAlertSep() string {
	if overlay.UseASCII() {
		return " - "
	}
	return " · "
}
