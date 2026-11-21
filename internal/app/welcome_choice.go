package app

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// The welcome question. On the first start, when tuios wrote a new
// config.toml, the welcome screen asks one question: Simple, Keyboard or tmux
// keys. Keys 1, 2 and 3 or a click pick one, and the choice goes into the
// file. Esc keeps today's tuios, which is Keyboard, and writes nothing.
// An existing install never sees the question.

// tmuxGlossary maps the two words tmux uses differently. The first start
// and the help panel show it, since a tmux user reads "window" as a tab.
const tmuxGlossary = "A window here is a tmux pane. A workspace is a tmux window."

// welcomeChoice is one answer.
type welcomeChoice struct {
	name, title, text string
}

var (
	welcomeSimple   = welcomeChoice{"simple", "Simple controls", "Click buttons to open, split and close panes."}
	welcomeKeyboard = welcomeChoice{"keyboard", "Keyboard", "Press Ctrl+B, then a key. Window mode moves panes."}
	welcomeTmux     = welcomeChoice{"tmux", "tmux keys", "Your tmux keys after Ctrl+B. No window mode."}
)

// welcomeChoices are the answers in the order the screen shows them. The
// tmux card comes first when tuios finds a tmux.conf.
func (m *OS) welcomeChoices() []welcomeChoice {
	if m.presetSuggestion().preset == "tmux" {
		return []welcomeChoice{welcomeTmux, welcomeSimple, welcomeKeyboard}
	}
	return []welcomeChoice{welcomeSimple, welcomeKeyboard, welcomeTmux}
}

// welcomeChoiceHit is the screen row a choice was drawn on.
type welcomeChoiceHit struct {
	Y, X0, X1 int
	Choice    int
}

// WelcomeQuestionShown reports whether the welcome screen asks the question
// now: the first start, no answer yet, and no pane on screen.
func (m *OS) WelcomeQuestionShown() bool {
	return m.welcomeAsk && len(m.GetVisibleWindows()) == 0
}

// welcomeQuestionLines are the question's lines for the splash, in a width.
func (m *OS) welcomeQuestionLines(width int) []string {
	ui := theme.UI()
	lines := []string{
		lipgloss.NewStyle().Foreground(ui.Fg).Bold(true).Render("How do you want to use tuios?"),
		lipgloss.NewStyle().Foreground(ui.FgDim).Render("Pick one. You can change it later in Settings."),
		"",
	}
	keys := []string{"1", "2", "3"}
	for i, c := range m.welcomeChoices() {
		head := overlay.KeyBadge(keys[i], ui) + " " + lipgloss.NewStyle().Foreground(ui.Accent).Bold(true).Render(c.title)
		body := lipgloss.NewStyle().Foreground(ui.FgDim).Render(c.text)
		line := head + "  " + body
		if lipgloss.Width(line) > width {
			line = head
		}
		lines = append(lines, line)
	}
	lines = append(lines, "", lipgloss.NewStyle().Foreground(ui.FgDim).Render("Press 1, 2 or 3, or click a choice. Esc keeps the keyboard."))
	lines = append(lines, "", lipgloss.NewStyle().Foreground(ui.FgDim).Render(ansi.Truncate(tmuxGlossary, width, "…")))
	return lines
}

// recordWelcomeChoices finds the choice rows in the drawn splash and keeps
// where they are, for a click. top and left are the splash's first cell.
func (m *OS) recordWelcomeChoices(splash string, left, top int) {
	m.welcomeHits = m.welcomeHits[:0]
	choices := m.welcomeChoices()
	for y, line := range strings.Split(splash, "\n") {
		plain := ansi.Strip(line)
		for i, c := range choices {
			at := strings.Index(plain, " "+c.title+"  ")
			if at < 0 && strings.HasSuffix(plain, " "+c.title) {
				at = len(plain) - len(c.title) - 1
			}
			if at < 0 {
				continue
			}
			x0 := left + ansi.StringWidth(plain[:at]) - 3
			m.welcomeHits = append(m.welcomeHits, welcomeChoiceHit{Y: top + y, X0: x0, X1: left + ansi.StringWidth(strings.TrimRight(plain, " ")), Choice: i})
		}
	}
}

// WelcomeChoiceAt is the choice drawn at the cell (x, y), or -1.
func (m *OS) WelcomeChoiceAt(x, y int) int {
	if !m.WelcomeQuestionShown() {
		return -1
	}
	for _, h := range m.welcomeHits {
		if y == h.Y && x >= h.X0 && x < h.X1 {
			return h.Choice
		}
	}
	return -1
}

// WelcomeChoiceKey answers a key while the question is up. It reports whether
// the key was an answer.
func (m *OS) WelcomeChoiceKey(key string) (tea.Cmd, bool) {
	if !m.WelcomeQuestionShown() {
		return nil, false
	}
	switch key {
	case "1", "2", "3":
		return m.ChooseWelcome(int(key[0] - '1')), true
	case "esc":
		m.welcomeAsk = false
		return nil, true
	}
	return nil, false
}

// welcomeSavedMsg says the config file was written with the answer.
type welcomeSavedMsg struct {
	title string
	err   error
}

// ChooseWelcome takes the answer at index i of the screen's order. Keyboard
// writes nothing. Simple writes profile = "simple". tmux keys writes the
// tmux preset with window mode off, and the leader a tmux.conf names.
func (m *OS) ChooseWelcome(i int) tea.Cmd {
	choices := m.welcomeChoices()
	if i < 0 || i >= len(choices) {
		return nil
	}
	m.welcomeAsk = false
	c := choices[i]
	if c.name == welcomeKeyboard.name || m.UserConfig == nil || m.ConfigReadOnly {
		return nil
	}
	switch c.name {
	case welcomeSimple.name:
		return m.SetProfile(config.ProfileSimple)
	case welcomeTmux.name:
		kb := &m.UserConfig.Keybindings
		off := false
		kb.Preset, kb.LeaderKey, kb.WindowMode = "tmux", m.presetSuggestion().leader, &off
	}
	write, err := config.RenderUserConfig(m.UserConfig)
	if err != nil {
		m.ShowNotification("The choice was not saved: "+err.Error(), "error", 0)
		return nil
	}
	write = m.queueSettingsSave(write)
	return func() tea.Msg {
		_, err := write()
		return welcomeSavedMsg{title: c.title, err: err}
	}
}

// SetProfile writes profile = name to the config file and reloads it, so the
// profile applies now. Keyboard is the default, so it takes the line out.
// The Settings row, the palette and the welcome question use it.
func (m *OS) SetProfile(name string) tea.Cmd {
	if m.UserConfig == nil || m.ConfigReadOnly {
		m.ShowNotification("This client cannot change the config file.", "warning", m.Settings.NotificationWarningDuration)
		return nil
	}
	title := "Keyboard controls"
	m.UserConfig.Profile = ""
	if name == config.ProfileSimple {
		title = "Simple controls"
		m.UserConfig.Profile = config.ProfileSimple
	}
	write, err := config.RenderUserConfig(m.UserConfig)
	if err != nil {
		m.ShowNotification("The choice was not saved: "+err.Error(), "error", 0)
		return nil
	}
	write = m.queueSettingsSave(write)
	return func() tea.Msg {
		_, err := write()
		return welcomeSavedMsg{title: title, err: err}
	}
}

// profileItem is the Settings row of the profile.
func (m *OS) profileItem() settingItem {
	item := boolItem("Simple controls", "Buttons on the dock, no window mode, and a workspace is a tab.",
		func() bool { return m.UserConfig.Simple() },
		func(m *OS, v bool) {})
	item.adjust = nil
	item.activate = func(m *OS) tea.Cmd {
		if m.UserConfig.Simple() {
			return m.SetProfile(config.ProfileKeyboard)
		}
		return m.SetProfile(config.ProfileSimple)
	}
	return item
}

// handleWelcomeSaved reloads the config the answer was written to.
func (m *OS) handleWelcomeSaved(msg welcomeSavedMsg) tea.Cmd {
	if msg.err != nil {
		config.RewindSave(m.UserConfig, msg.err)
		m.ShowNotification("The choice was not saved: "+msg.err.Error(), "error", 0)
		return nil
	}
	m.ShowNotification(msg.title+" are on. Change them in Settings, under Behavior.", "success", m.Settings.NotificationWarningDuration)
	return m.ReloadConfigFromDisk()
}
