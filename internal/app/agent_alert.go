package app

import (
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
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
	if policy.SuppressFocused && m.GetFocusedWindow() == w {
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
		if policy.SuppressFocused && m.GetFocusedWindow() == w {
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
	name := printableTitle(m.railTitleShown(w))
	if name == "" {
		name = "pane"
	}
	text := name + " " + word

	if policy.Dock {
		m.ShowNotificationFrom(text, sev, config.NotificationDuration,
			NotifTarget{SessionID: m.sidebarCurrentSessionID(), WindowID: w.ID})
	}
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

	// The cue plays from the client, which is the process with a human in front
	// of it. Under `tuios ssh` that is the laptop rather than the host the
	// session runs on, and it falls out of where alerts already live rather than
	// needing a wire message. Play returns before anything is spawned, so the
	// Update goroutine this runs on is not waiting on an audio device.
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
