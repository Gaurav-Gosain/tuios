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
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// The keybind preset picker: one dialog for the welcome row, the palette and
// the keybind manager. Each row is a preset with a line of text and three
// sample chords. The footer says which keys the program in a pane would lose
// to the highlighted preset, and where the tuios keys it moves are. Enter
// writes keybindings.preset and reloads the config. Nothing here switches a
// preset without the person pressing Enter.

// presetPickerState is the picker's state, kept on the OS.
type presetPickerState struct {
	open     bool
	selected int
	scroll   int
	// loses is, for each preset, the keys a pane would lose to it that it
	// does not lose today. Built when the picker opens, not per frame.
	loses map[string][]string
	// leader is the leader the picker writes with the preset: empty for the
	// preset's own, or the one sniffed from ~/.tmux.conf.
	leader string
	// about and leaders are each preset's text and leader, read when the
	// picker opens: each is a TOML decode, too slow for every frame.
	about   []config.PresetAbout
	leaders []string
}

// keybindPresetSavedMsg says the config file was written with the new preset.
type keybindPresetSavedMsg struct {
	name string
	err  error
	// prevPreset and prevLeader are put back when the write fails.
	prevPreset, prevLeader string
}

// OpenKeybindPresets opens the picker on the preset the welcome row suggests,
// or on the preset in use.
func (m *OS) OpenKeybindPresets() {
	p := &m.presetPicker
	p.open = true
	p.scroll = 0
	p.loses = presetLosses(m.currentPreset())
	names := config.Presets()
	p.about, p.leaders = make([]config.PresetAbout, len(names)), make([]string, len(names))
	for i, name := range names {
		p.about[i], _ = config.PresetInfo(name)
		p.leaders[i] = config.DefaultKeybindingsFor(name).LeaderKey
	}
	p.selected = max(slices.Index(names, m.currentPreset()), 0)
	if s := m.presetSuggestion(); s.preset != "" {
		p.selected = max(slices.Index(names, s.preset), 0)
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
	livePickerMove(&p.selected, &p.scroll, delta, len(config.Presets()), visible, m.listWraps())
}

// PresetPickerApply writes the highlighted preset and reloads the config. A
// served client cannot: the config file is the operator's.
func (m *OS) PresetPickerApply() tea.Cmd {
	p := &m.presetPicker
	names := config.Presets()
	if p.selected < 0 || p.selected >= len(names) || m.UserConfig == nil {
		return nil
	}
	if m.LearnMode {
		// The tour has no config file to write. The preset changes the keys
		// of this tab, so a lesson can show them at work.
		p.open = false
		return m.LearnApplyPreset(names[p.selected])
	}
	if m.ConfigReadOnly {
		m.ShowNotification("This client cannot change the keys. Run tuios keybinds preset on the server.", "warning", m.Settings.NotificationWarningDuration)
		return nil
	}
	name := names[p.selected]
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
	// The leader goes with the preset, unless the person's tmux.conf names
	// another one: see presetSuggestion.
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

// handleKeybindPresetSaved reloads the config the preset was written to, so
// the keys change in this client now. Other clients follow the file.
func (m *OS) handleKeybindPresetSaved(msg keybindPresetSavedMsg) tea.Cmd {
	if msg.err != nil {
		// The preset is in no file and the keys did not change, so the
		// config goes back to what it was, and a later save does not
		// write the preset as a side effect.
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
		text += " Press " + menu + " for the tuios keys it moved."
	}
	m.ShowNotification(text, "success", m.Settings.NotificationWarningDuration)
	return cmd
}

// currentPreset is the preset in use, tuios when none is named.
func (m *OS) currentPreset() string {
	if m.UserConfig == nil || m.UserConfig.Keybindings.Preset == "" {
		return config.PresetTuios
	}
	return m.UserConfig.Keybindings.Preset
}

// presetLosses is, for each preset, the keys a pane loses to it that it does
// not lose under the preset in use.
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

// presetSuggestion is what the welcome screen offers: a preset for the
// multiplexer whose config is in the home folder, with the leader a tmux.conf
// names. It is only a suggestion. Nothing switches until the person picks.
type presetSuggestion struct {
	preset, leader, label string
}

// tmuxPrefixLine finds the prefix a tmux.conf sets, such as set -g prefix C-a,
// set-option -g prefix 'C-Space' or set -g prefix ^A.
var tmuxPrefixLine = regexp.MustCompile(`(?m)^[ \t]*set(?:-option)?[ \t]+(?:-\w+[ \t]+)*prefix[ \t]+['"]?(?:C-|\^)([A-Za-z]|[Ss]pace)['"]?(?:[ \t]|$)`)

// tmuxConfLimit bounds the read of a tmux.conf. A prefix line is near the top,
// and the file is the person's own but could be anything: a link to a device,
// a pipe or a huge generated file.
const tmuxConfLimit = 64 << 10

// tmuxPrefix reads the prefix a tmux.conf sets: tmux keeps the last one. It
// reads a regular file only, and only its first tmuxConfLimit bytes.
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

// presetSuggestion reads the home folder once per client. A served client is
// on the operator's machine, whose home says nothing about the person, so it
// gets no suggestion; nor does a client already on a preset.
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

// welcomePresetChip is the welcome screen's preset hint: the key that opens
// the picker and the suggestion. Empty when there is none to show.
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
