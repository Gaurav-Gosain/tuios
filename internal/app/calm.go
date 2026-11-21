package app

import (
	"fmt"
	"strconv"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

type calmState struct {
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
	if on {
		m.ShowNotification("Calm mode on.", "info", m.Settings.NotificationDuration)
	} else if m.calm.held == 0 && len(m.Notifications) == 0 {
		m.ShowNotification("Calm mode off.", "info", m.Settings.NotificationDuration)
	}
	return m.persistSettings()
}

func (m *OS) calmHolds(notifType, agentState string, target *NotifTarget) bool {
	if !m.Settings.Calm || target == nil || notifSeverityRank(notifType) >= 2 {
		return false
	}
	if agentState != "" && calmNeedsYou(agentState) {
		return false
	}
	if target.DaemonRestart || target.ClipboardAsk != 0 {
		return false
	}
	return true
}

func calmNeedsYou(state string) bool {
	return config.AgentAlertPolicy{}.AttentionCue(state)
}

func (m *OS) calmDockLabel() string {
	if !m.Settings.Calm {
		return ""
	}
	if m.calm.held > 0 {
		return "calm " + strconv.Itoa(m.calm.held)
	}
	return "calm"
}

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

func (m *OS) showRemoteNotice(text string) {
	if m.Settings.Calm {
		m.LogInfo("%s", text)
		return
	}
	m.ShowNotification(text, "info", m.Settings.NotificationDuration)
}
