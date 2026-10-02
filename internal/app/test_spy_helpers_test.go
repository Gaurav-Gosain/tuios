package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/hooks"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// withSpyInputHandler swaps in a handler that records whether it was reached,
// and puts the old one back afterwards.
func withSpyInputHandler(t *testing.T) *bool {
	t.Helper()
	previous := getInputHandler()
	reached := false
	SetInputHandler(func(_ tea.Msg, m *OS) (tea.Model, tea.Cmd) {
		reached = true
		return m, nil
	})
	t.Cleanup(func() {
		if previous != nil {
			SetInputHandler(previous)
		}
	})
	return &reached
}

// alertOS builds a client with two panes and a policy, with nothing focused so
// suppress_focused never hides an alert the test meant to see.
func alertOS(t *testing.T, agent config.AgentAlertsConfig) *OS {
	t.Helper()
	m := &OS{
		Settings: config.Global,
		Width:    120, Height: 40,
		FocusedWindow:    -1,
		NumWorkspaces:    9,
		CurrentWorkspace: 1,
		UserConfig:       &config.UserConfig{},
		HookManager:      hooks.NewManager(),
	}
	m.UserConfig.Notifications.Agent = agent
	for _, id := range []string{"w-1", "w-2"} {
		w := &terminal.Window{ID: id, CustomName: id, Workspace: 1}
		m.Windows = append(m.Windows, w)
	}
	return m
}

// hostCapture stands in for the terminal on the far end of the render stream.
type hostCapture struct{ b strings.Builder }

func (h *hostCapture) Write(p []byte) (int, error) { return h.b.Write(p) }

// captureHost points the client's raw host writes at a buffer.
func captureHost(t *testing.T, m *OS) *hostCapture {
	t.Helper()
	h := &hostCapture{}
	m.KittyPassthrough = NewKittyPassthroughWithOptions(KittyPassthroughOptions{Output: h})
	return h
}

func zeroSettle() config.AgentAlertsConfig {
	settle := 0
	return config.AgentAlertsConfig{SettleSeconds: &settle}
}
