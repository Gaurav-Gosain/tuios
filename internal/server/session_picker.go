// Package server provides session picker UI for SSH and Web servers.
package server

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

// SessionPickerResult represents the user's choice from the session picker.
type SessionPickerResult struct {
	SessionName string // Selected or new session name
	CreateNew   bool   // Whether to create a new session
	Cancelled   bool   // User cancelled the picker
}

// SessionPicker is a Bubble Tea model for selecting or creating a session.
type SessionPicker struct {
	sessions       []session.SessionInfo
	cursor         int
	width          int
	height         int
	result         *SessionPickerResult
	newSessionName string
	inputMode      bool // True when typing a new session name
	error          string
}

// NewSessionPicker creates a new session picker with the given session list.
func NewSessionPicker(sessions []session.SessionInfo) *SessionPicker {
	return &SessionPicker{
		sessions: sessions,
		cursor:   0,
		result:   nil,
	}
}

// Init implements tea.Model.
func (m *SessionPicker) Init() tea.Cmd {
	return nil
}

// Update implements tea.Model.
func (m *SessionPicker) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return m.handleKeyPress(msg)

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	}

	return m, nil
}

func (m *SessionPicker) handleKeyPress(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.inputMode {
		return m.handleInputMode(msg)
	}

	switch {
	case msg.String() == "q", msg.String() == "esc", msg.String() == "ctrl+c":
		m.result = &SessionPickerResult{Cancelled: true}
		return m, tea.Quit

	case msg.String() == "up", msg.String() == "k":
		if m.cursor > 0 {
			m.cursor--
		}

	case msg.String() == "down", msg.String() == "j":
		// +1 for "New Session" option
		if m.cursor < len(m.sessions) {
			m.cursor++
		}

	case msg.String() == "enter":
		if m.cursor == len(m.sessions) {
			// "New Session" selected - enter input mode
			m.inputMode = true
			m.newSessionName = ""
			m.error = ""
		} else {
			// Existing session selected
			m.result = &SessionPickerResult{
				SessionName: m.sessions[m.cursor].Name,
				CreateNew:   false,
			}
			return m, tea.Quit
		}

	case msg.String() == "n":
		// Quick shortcut for new session
		m.inputMode = true
		m.newSessionName = ""
		m.error = ""
	}

	return m, nil
}

func (m *SessionPicker) handleInputMode(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch {
	case msg.String() == "esc":
		m.inputMode = false
		m.newSessionName = ""
		m.error = ""

	case msg.String() == "enter":
		name := strings.TrimSpace(m.newSessionName)
		if name == "" {
			// Generate a unique name
			name = m.generateSessionName()
		}
		// Check if name already exists
		for _, s := range m.sessions {
			if s.Name == name {
				m.error = fmt.Sprintf("Session '%s' already exists", name)
				return m, nil
			}
		}
		m.result = &SessionPickerResult{
			SessionName: name,
			CreateNew:   true,
		}
		return m, tea.Quit

	case msg.String() == "backspace":
		if len(m.newSessionName) > 0 {
			m.newSessionName = m.newSessionName[:len(m.newSessionName)-1]
		}

	default:
		// Add character to session name (only allow valid characters)
		r := msg.String()
		if len(r) == 1 && isValidSessionNameChar(rune(r[0])) {
			m.newSessionName += r
		}
	}

	return m, nil
}

func isValidSessionNameChar(r rune) bool {
	return (r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z') ||
		(r >= '0' && r <= '9') ||
		r == '-' || r == '_'
}

func (m *SessionPicker) generateSessionName() string {
	existing := make(map[string]bool)
	for _, s := range m.sessions {
		existing[s.Name] = true
	}
	for i := 0; ; i++ {
		name := fmt.Sprintf("session-%d", i)
		if !existing[name] {
			return name
		}
	}
}

// Result returns the user's selection after the picker has quit.
func (m *SessionPicker) Result() *SessionPickerResult {
	return m.result
}

// View implements tea.Model.
func (m *SessionPicker) View() tea.View {
	var view tea.View

	// Styles
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("39")).
		MarginBottom(1)

	selectedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("229")).
		Background(lipgloss.Color("57")).
		Bold(true).
		Padding(0, 1)

	normalStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("252")).
		Padding(0, 1)

	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("240"))

	errorStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("196")).
		Bold(true)

	inputStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("229")).
		Background(lipgloss.Color("236")).
		Padding(0, 1)

	var b strings.Builder

	// Title
	b.WriteString(titleStyle.Render("TUIOS Sessions"))
	b.WriteString("\n\n")

	// Input mode for new session
	if m.inputMode {
		b.WriteString("Enter session name (or press Enter for auto-generated):\n\n")
		cursor := "█"
		b.WriteString(inputStyle.Render(m.newSessionName + cursor))
		b.WriteString("\n\n")
		if m.error != "" {
			b.WriteString(errorStyle.Render(m.error))
			b.WriteString("\n\n")
		}
		b.WriteString(dimStyle.Render("Enter: confirm  |  Esc: cancel"))
		view.SetContent(b.String())
		view.AltScreen = true
		return view
	}

	// Session list
	if len(m.sessions) == 0 {
		b.WriteString(dimStyle.Render("No existing sessions"))
		b.WriteString("\n\n")
	} else {
		for i, s := range m.sessions {
			var line string
			status := "detached"
			if s.Attached {
				status = "attached"
			}
			lastActive := time.Since(time.Unix(s.LastActive, 0)).Truncate(time.Second)
			details := fmt.Sprintf("(%d windows, %s, %s ago)", s.WindowCount, status, lastActive)

			if i == m.cursor {
				line = selectedStyle.Render("▸ " + s.Name)
				line += " " + dimStyle.Render(details)
			} else {
				line = normalStyle.Render("  " + s.Name)
				line += " " + dimStyle.Render(details)
			}
			b.WriteString(line)
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}

	// New session option
	newSessionLabel := "  + New Session"
	if m.cursor == len(m.sessions) {
		newSessionLabel = selectedStyle.Render("▸ + New Session")
	} else {
		newSessionLabel = normalStyle.Render(newSessionLabel)
	}
	b.WriteString(newSessionLabel)
	b.WriteString("\n\n")

	// Help
	b.WriteString(dimStyle.Render("↑/k: up  |  ↓/j: down  |  Enter: select  |  n: new  |  q/Esc: cancel"))

	view.SetContent(b.String())
	view.AltScreen = true
	return view
}
