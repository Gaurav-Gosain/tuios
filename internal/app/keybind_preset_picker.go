package app

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/listnav"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

type presetPickerState struct {
	open    bool
	list    listnav.State
	loses   map[string][]string
	leader  string
	about   []config.PresetAbout
	leaders []string
}

type keybindPresetSavedMsg struct {
	name                   string
	err                    error
	prevPreset, prevLeader string
}

// OpenKeybindPresets opens the picker on the preset the welcome row suggests,
// or on the preset in use.
func (m *OS) OpenKeybindPresets() {
	p := &m.presetPicker
	p.open = true
	p.list.Scroll = 0
	p.loses = presetLosses(m.currentPreset())
	names := config.Presets()
	p.about, p.leaders = make([]config.PresetAbout, len(names)), make([]string, len(names))
	for i, name := range names {
		p.about[i], _ = config.PresetInfo(name)
		p.leaders[i] = config.DefaultKeybindingsFor(name).LeaderKey
	}
	p.list.Cursor = max(slices.Index(names, m.currentPreset()), 0)
	if s := m.presetSuggestion(); s.preset != "" {
		p.list.Cursor = max(slices.Index(names, s.preset), 0)
		p.leader = s.leader
	}
}

// ClosePresetPicker closes the picker without a change.
func (m *OS) ClosePresetPicker() { m.presetPicker.open = false }

// PresetPickerOpen reports whether the picker is open.
func (m *OS) PresetPickerOpen() bool { return m.presetPicker.open }

// PresetPickerMove moves the cursor.
func (m *OS) PresetPickerMove(delta int) {
	p := &m.presetPicker
	_, visible, _ := m.presetPickerLayout()
	p.list.Move(delta, len(config.Presets()), visible, m.listWraps(), nil)
}

// PresetPickerApply writes the highlighted preset and reloads the config. A
// served client cannot: the config file is the operator's.
func (m *OS) PresetPickerApply() tea.Cmd {
	p := &m.presetPicker
	names := config.Presets()
	if p.list.Cursor < 0 || p.list.Cursor >= len(names) || m.UserConfig == nil {
		return nil
	}
	if m.LearnMode {
		p.open = false
		return m.LearnApplyPreset(names[p.list.Cursor])
	}
	if m.ConfigReadOnly {
		m.ShowNotification("This client cannot change the keys. Run tuios keybinds preset on the server.", "warning", m.Settings.NotificationWarningDuration)
		return nil
	}
	name := names[p.list.Cursor]
	p.open = false
	if name == m.currentPreset() {
		return nil
	}
	kb := &m.UserConfig.Keybindings
	prevPreset, prevLeader := kb.Preset, kb.LeaderKey
	kb.Preset = name
	if name == config.PresetTuios {
		kb.Preset = ""
	}
	kb.LeaderKey = ""
	if name == "tmux" {
		kb.LeaderKey = p.leader
	}
	write, err := config.RenderUserConfig(m.UserConfig)
	if err != nil {
		m.ShowNotification("Keys not changed: "+err.Error(), "error", 0)
		return nil
	}
	write = m.queueSettingsSave(write)
	return func() tea.Msg {
		_, err := write()
		return keybindPresetSavedMsg{name: name, err: err, prevPreset: prevPreset, prevLeader: prevLeader}
	}
}

func (m *OS) handleKeybindPresetSaved(msg keybindPresetSavedMsg) tea.Cmd {
	if msg.err != nil {
		if m.UserConfig != nil {
			m.UserConfig.Keybindings.Preset = msg.prevPreset
			m.UserConfig.Keybindings.LeaderKey = msg.prevLeader
			config.RewindSave(m.UserConfig, msg.err)
		}
		m.ShowNotification("Keys not changed: "+msg.err.Error(), "error", 0)
		return nil
	}
	cmd := m.ReloadConfigFromDisk()
	text := "The " + msg.name + " keys are in use."
	if msg.name != config.PresetTuios {
		menu := m.Settings.LeaderKey + " ctrl+t"
		text += " Press " + menu + " to see the tuios keys that this preset moved."
	}
	m.ShowNotification(text, "success", m.Settings.NotificationWarningDuration)
	return cmd
}

func (m *OS) currentPreset() string {
	if m.UserConfig == nil || m.UserConfig.Keybindings.Preset == "" {
		return config.PresetTuios
	}
	return m.UserConfig.Keybindings.Preset
}

func presetLosses(current string) map[string][]string {
	swallowed := func(name string) map[string]string {
		cfg := config.DefaultConfig()
		cfg.Keybindings = config.DefaultKeybindingsFor(name)
		out := map[string]string{}
		for _, s := range config.NewKeybindRegistry(cfg).TerminalModeSwallowed() {
			out[config.CanonicalKey(s.Key)] = s.Key
		}
		return out
	}
	now := swallowed(current)
	losses := map[string][]string{}
	for _, name := range config.Presets() {
		var keys []string
		for canon, key := range swallowed(name) {
			if _, already := now[canon]; !already {
				keys = append(keys, key)
			}
		}
		slices.Sort(keys)
		losses[name] = keys
	}
	return losses
}

type presetSuggestion struct {
	preset, leader, label string
}

var tmuxPrefixLine = regexp.MustCompile(`(?m)^[ \t]*set(?:-option)?[ \t]+(?:-\w+[ \t]+)*prefix[ \t]+['"]?(?:C-|\^)([A-Za-z]|[Ss]pace)['"]?(?:[ \t]|$)`)

const tmuxConfLimit = 64 << 10

func tmuxPrefix(path string) string {
	f, err := os.Open(path) // #nosec G304 - the person's own tmux.conf, read for one line
	if err != nil {
		return ""
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || !st.Mode().IsRegular() {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(f, tmuxConfLimit))
	if err != nil {
		return ""
	}
	all := tmuxPrefixLine.FindAllSubmatch(data, -1)
	if len(all) == 0 {
		return ""
	}
	key := strings.ToLower(string(all[len(all)-1][1]))
	if key == "b" {
		return ""
	}
	return "ctrl+" + key
}

func (m *OS) presetSuggestion() presetSuggestion {
	if m.presetHint != nil {
		return *m.presetHint
	}
	s := presetSuggestion{}
	m.presetHint = &s
	if m.Client.Remote() || m.currentPreset() != config.PresetTuios || m.Client == ClientUnknown {
		return s
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return s
	}
	exists := func(rel string) bool {
		_, err := os.Stat(filepath.Join(home, rel))
		return err == nil
	}
	for _, rel := range []string{".tmux.conf", ".config/tmux/tmux.conf"} {
		if !exists(rel) {
			continue
		}
		s.preset, s.label = "tmux", "use tmux keys"
		if s.leader = tmuxPrefix(filepath.Join(home, rel)); s.leader != "" {
			s.label = "use tmux keys with " + s.leader
		}
		return s
	}
	switch {
	case exists(".screenrc"):
		s.preset, s.label = "screen", "use screen keys"
	case exists(".config/zellij"):
		s.preset, s.label = "zellij", "use zellij keys"
	default:
		s.label = "pick keys"
	}
	return s
}

func (m *OS) welcomePresetChip() overlay.Hint {
	label := strings.TrimSpace(m.presetSuggestion().label)
	if label == "" || m.UserConfig == nil || m.currentPreset() != config.PresetTuios {
		return overlay.Hint{}
	}
	keys := m.UserConfig.Keybindings.ModeControl["open_keybind_presets"]
	if len(keys) == 0 {
		return overlay.Hint{}
	}
	return overlay.Hint{Key: keys[0], Label: label}
}
