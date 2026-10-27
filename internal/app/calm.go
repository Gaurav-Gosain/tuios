package app

import (
	"fmt"
	"strconv"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// Calm mode quiets the screen for somebody who wants to stay with one task.
// It is one switch over things that already exist, not a new mechanism:
//
//   - Motion. config.Settings.MotionAllows answers false for every level while
//     calm is on, so every slide, fade, shimmer, sweep and confetti that asks
//     it is a still frame. appearance.motion is left as it was, so turning calm
//     off gives back the level the person chose.
//   - Messages. An info or success message that a pane, an agent or mail sent
//     is held: it goes to the log and to a count the dock shows. A warning, an
//     error, and tuios's own answer to a key the person pressed still show,
//     and so does an agent that needs the person (needs_input, errored). See
//     calmHolds and fireAgentAlert.
//   - Blinking. The cursor is drawn steady, and the dock's workspace tabs take
//     no activity mark (the bell mark stays).
//
// Nothing here runs on a timer. Calm on costs fewer frames, not more: with an
// agent working, the shimmer clock is what wakes the client 8 times a second,
// and calm never arms it.

// calmState is what calm mode holds at runtime. The switch itself is
// m.Settings.Calm, applied from appearance.calm.
type calmState struct {
	// held counts the messages kept off the dock since calm came on.
	held int
}

// CalmOn reports whether calm mode is on for this client.
func (m *OS) CalmOn() bool { return m.Settings.Calm }

// SetCalm puts calm mode in one state and records it in the config, without
// saving. The settings row uses it, and ToggleCalm does. Turning it off says
// how many messages were held, once, and resets the count.
func (m *OS) SetCalm(on bool) {
	was := m.Settings.Calm
	m.setOption("appearance.calm", strconv.FormatBool(on))
	m.Settings.Calm = on
	if was == on {
		return
	}
	m.MarkAllDirty()
	if on {
		m.calm.held = 0
		return
	}
	held := m.calm.held
	m.calm.held = 0
	if held > 0 {
		text := "Calm mode off. 1 message is in the log."
		if held > 1 {
			text = fmt.Sprintf("Calm mode off. %d messages are in the log.", held)
		}
		m.ShowNotification(text, "info", 2*m.Settings.NotificationDuration)
	}
}

// ToggleCalm flips calm mode, says so, and saves the choice. Shared by the
// key, the command palette and run-command.
func (m *OS) ToggleCalm() tea.Cmd {
	on := !m.Settings.Calm
	m.SetCalm(on)
	// The answer to the person's own key, so it shows in calm mode too.
	if on {
		m.ShowNotification("Calm mode on.", "info", m.Settings.NotificationDuration)
	} else if m.calm.held == 0 && len(m.Notifications) == 0 {
		m.ShowNotification("Calm mode off.", "info", m.Settings.NotificationDuration)
	}
	return m.persistSettings()
}

// calmHolds reports whether calm mode keeps this message off the dock. Only
// an info or success message that something other than the person's own key
// raised is held: target is set for a message from a pane, an agent or mail,
// and nil for tuios's own feedback. An agent message is held unless the state
// it announces needs the person.
func (m *OS) calmHolds(notifType, agentState string, target *NotifTarget) bool {
	if !m.Settings.Calm || target == nil || notifSeverityRank(notifType) >= 2 {
		return false
	}
	if agentState != "" && calmNeedsYou(agentState) {
		return false
	}
	// A notice about this client's own build is not an interruption from a
	// pane; it stays.
	if target.DaemonRestart || target.ClipboardAsk != 0 {
		return false
	}
	return true
}

// calmNeedsYou reports whether an agent state is one calm mode still shows:
// the agent stopped and waits on the person.
func calmNeedsYou(state string) bool {
	return config.AgentAlertPolicy{}.AttentionCue(state)
}

// calmDockLabel is the dock's calm mark: "calm" while it is on, and the held
// count after it when messages are waiting in the log. Empty when calm is off.
func (m *OS) calmDockLabel() string {
	if !m.Settings.Calm {
		return ""
	}
	if m.calm.held > 0 {
		return "calm " + strconv.Itoa(m.calm.held)
	}
	return "calm"
}

// calmChip is the dock's calm mark, drawn after the mode pill while calm is
// on, or "" while it is off. Quiet ink on the dock's own ground: it says a
// state the person chose and asks for nothing.
func (m *OS) calmChip() string {
	label := m.calmDockLabel()
	if label == "" {
		return ""
	}
	pal := m.groundUI()
	return overlay.Style(pal.Panel).Render(" ") +
		lipgloss.NewStyle().Background(pal.Panel).Foreground(pal.FgDim).Render(label) +
		overlay.Style(pal.Panel).Render(" ")
}

// showRemoteNotice puts up the echo of a command another process ran in this
// client ("Remote: send-keys Enter"). In calm mode the echo is only logged:
// an agent driving the session would otherwise keep the dock busy, and the
// command's own effect is on screen anyway. It is not counted as held, since
// nothing in it waits to be read.
func (m *OS) showRemoteNotice(text string) {
	if m.Settings.Calm {
		m.LogInfo("%s", text)
		return
	}
	m.ShowNotification(text, "info", m.Settings.NotificationDuration)
}
