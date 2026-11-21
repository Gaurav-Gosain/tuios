package guibridge

import (
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// Attention is the "attention" event: every open Inbox item, in Inbox order.
type Attention struct {
	// Live is false until the mirror holds a listing, and again while the
	// daemon cannot be reached. Items then are the last ones known.
	Live  bool                    `json:"live"`
	Items []session.AttentionItem `json:"items"`
}

// Notify is the "notify" event: one alert the model raised.
type Notify struct {
	ID      string `json:"id"`
	Message string `json:"message"`
	// Level is info, success, warning or error.
	Level string `json:"level"`
	// AgentState is the agent state the alert announces, empty for any other
	// message.
	AgentState string `json:"agent_state,omitempty"`
	// Host, Session and Window name the pane the alert is about, empty when
	// it is about none.
	Host    string `json:"host,omitempty"`
	Session string `json:"session,omitempty"`
	Window  string `json:"window,omitempty"`
	// Desktop is the person's alert policy for a desktop notification now:
	// alerts on, desktop notifications on, and outside the quiet hours. The
	// renderer shows one only while its window is not active.
	Desktop bool `json:"desktop"`
}

// Detached is the "detached" event, sent once before the bridge exits when
// the daemon ended the attach.
type Detached struct {
	// Reason is session_ended, detached, daemon_lost, host_lost or refused.
	Reason  string `json:"reason"`
	Message string `json:"message"`
}

func (m *model) exportAttention() {
	in := &m.os.Inbox
	if m.attentionSent && in.Gen == m.attentionGen && in.Live == m.attentionLive {
		return
	}
	m.attentionSent, m.attentionGen, m.attentionLive = true, in.Gen, in.Live
	items := in.Items
	if items == nil {
		items = []session.AttentionItem{}
	}
	m.out.JSON(Event{Type: "attention", Attention: &Attention{Live: in.Live, Items: items}})
}

func (m *model) exportNotify() {
	live := make(map[string]bool, len(m.os.Notifications))
	for _, n := range m.os.Notifications {
		live[n.ID] = true
		if m.notified[n.ID] {
			continue
		}
		ev := Notify{ID: n.ID, Message: n.Message, Level: n.Type, AgentState: n.AgentState}
		if n.Target != nil {
			ev.Host, ev.Session, ev.Window = n.Target.Host, n.Target.SessionID, n.Target.WindowID
		}
		ev.Desktop = m.os.DesktopAlerts()
		m.out.JSON(Event{Type: "notify", Notify: &ev})
	}
	m.notified = live
}

func detachedEvent(o *app.OS) *Detached {
	switch o.ExitReason {
	case app.ExitSessionKilled:
		name := o.SessionName
		if name == "" {
			name = "this session"
		}
		return &Detached{Reason: "session_ended", Message: "The session " + name + " stopped."}
	case app.ExitDetached:
		msg := "A different client detached this one."
		if o.DaemonClient != nil && o.DaemonClient.DetachedReason() != "" {
			msg = o.DaemonClient.DetachedReason()
		}
		return &Detached{Reason: "detached", Message: msg}
	case app.ExitDaemonLost:
		return &Detached{Reason: "daemon_lost", Message: "The connection to the tuios daemon closed."}
	case app.ExitDaemonUpgraded:
		return &Detached{Reason: "daemon_upgraded", Message: "The tuios daemon is being upgraded. Every program is still running. Attach again to continue."}
	case app.ExitHostLost:
		return &Detached{Reason: "host_lost", Message: "The link to " + o.AttachedHost + " closed."}
	case app.ExitNestedRefused:
		msg := "The daemon refused this attach."
		if o.DaemonClient != nil && o.DaemonClient.NestedRefusal() != "" {
			msg = o.DaemonClient.NestedRefusal()
		}
		return &Detached{Reason: "refused", Message: msg}
	}
	return nil
}
