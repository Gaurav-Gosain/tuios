package config

import (
	"bytes"
	"embed"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Keybind presets: the keys of another multiplexer, for a person whose hands
// already know them. keybindings.preset names one.
//
// A preset is an embedded file in the config's own syntax, a delta over the
// tuios defaults (see presets/tmux.toml). It is applied to the defaults before
// the user's own config is filled from them, so from then on the preset is the
// default: the keybind manager's reset, keybinds doctor, the which-key rows
// and the duplicate repair all read it, and a preset's empty list is not a key
// the user took away.
//
// A preset changes only the leader, the keys after the leader and the
// terminal-mode keys. Window mode, the rail, copy mode and the Inbox keep the
// tuios keys. A tuios feature whose key the preset uses moves to the tuios
// menu, the leader then ctrl+t, under the key it had.
//
// The user's own config wins over the preset. An action the user binds keeps
// the user's keys, and a key the user binds keeps its action: the preset's
// action for that key is left without it (see fillMapDefaultsYielding).

// PresetTuios is the preset of the tuios defaults themselves.
const PresetTuios = "tuios"

//go:embed presets/*.toml
var presetFiles embed.FS

// PresetAbout is what a preset says about itself, for the cheat sheet and the
// preset picker. It is not config.
type PresetAbout struct {
	Name    string   `toml:"name"`
	Summary string   `toml:"summary"`
	Samples []string `toml:"samples"`
	Model   string   `toml:"model"`
	Same    []string `toml:"same"`
	Missing []string `toml:"missing"`
	Notes   []string `toml:"notes"`
}

// keybindPreset is one preset file.
type keybindPreset struct {
	LeaderKey    string              `toml:"leader_key"`
	LeaderTwice  string              `toml:"leader_twice"`
	Repeat       []string            `toml:"repeat"`
	About        PresetAbout         `toml:"about"`
	PrefixMode   map[string][]string `toml:"prefix_mode"`
	TerminalMode map[string][]string `toml:"terminal_mode"`
	TuiosPrefix  map[string][]string `toml:"tuios_prefix"`
	// The window and workspace menus, for a preset whose mode keys open
	// them (zellij's pane and tab modes).
	WindowPrefix    map[string][]string `toml:"window_prefix"`
	WorkspacePrefix map[string][]string `toml:"workspace_prefix"`
}

// Presets lists the preset names, tuios first.
func Presets() []string {
	entries, _ := presetFiles.ReadDir("presets")
	names := []string{PresetTuios}
	for _, e := range entries {
		names = append(names, strings.TrimSuffix(e.Name(), ".toml"))
	}
	// The multiplexers by how many people come from them, then any other.
	rank := func(n string) int {
		if i := slices.Index([]string{PresetTuios, "tmux", "screen", "zellij"}, n); i >= 0 {
			return i
		}
		return 99
	}
	rest := names[1:]
	sort.SliceStable(rest, func(i, j int) bool { return rank(rest[i]) < rank(rest[j]) })
	return names
}

// loadPreset reads one preset file. The tuios preset is no file.
func loadPreset(name string) (*keybindPreset, error) {
	data, err := presetFiles.ReadFile("presets/" + name + ".toml")
	if err != nil {
		return nil, fmt.Errorf("%q is not a keybind preset. Use one of: %s", name, strings.Join(Presets(), ", "))
	}
	var p keybindPreset
	dec := toml.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return nil, fmt.Errorf("keybind preset %s: %w", name, err)
	}
	return &p, nil
}

// PresetInfo is what a preset says about itself. The tuios preset has a name
// and a summary only.
func PresetInfo(name string) (PresetAbout, error) {
	if name == "" || name == PresetTuios {
		return PresetAbout{Name: PresetTuios, Summary: "the tuios keys", Samples: []string{"n", "t", "w"}}, nil
	}
	p, err := loadPreset(name)
	if err != nil {
		return PresetAbout{}, err
	}
	return p.About, nil
}

// PresetSource returns a preset file as tuios ships it, for tuios keybinds
// preset show.
func PresetSource(name string) ([]byte, error) {
	if name == "" || name == PresetTuios {
		return nil, fmt.Errorf("the tuios preset is the defaults themselves. Run tuios keybinds list")
	}
	if _, err := loadPreset(name); err != nil {
		return nil, err
	}
	return presetFiles.ReadFile("presets/" + name + ".toml")
}

// applyPreset writes a preset over the default keybindings kb. An unknown
// name leaves kb as it is and says so.
func applyPreset(kb *KeybindingsConfig, name string) error {
	if name == "" || name == PresetTuios {
		return nil
	}
	p, err := loadPreset(name)
	if err != nil {
		return err
	}
	if p.LeaderKey != "" {
		kb.LeaderKey = p.LeaderKey
	}
	if p.Repeat != nil {
		kb.Repeat = slices.Clone(p.Repeat)
	}
	if p.LeaderTwice != "" {
		kb.LeaderTwice = p.LeaderTwice
	}
	kb.PrefixMode = overlayPresetSection(kb.PrefixMode, p.PrefixMode)
	kb.TerminalMode = overlayPresetSection(kb.TerminalMode, p.TerminalMode)
	kb.TuiosPrefix = overlayPresetSection(kb.TuiosPrefix, p.TuiosPrefix)
	kb.WindowPrefix = overlayPresetSection(kb.WindowPrefix, p.WindowPrefix)
	kb.WorkspacePrefix = overlayPresetSection(kb.WorkspacePrefix, p.WorkspacePrefix)
	return nil
}

// overlayPresetSection is the defaults of one section with a preset's actions
// written over them, as a new map: the defaults are shared with every other
// caller of DefaultConfig.
func overlayPresetSection(defaults, preset map[string][]string) map[string][]string {
	out := maps.Clone(defaults)
	if out == nil {
		out = map[string][]string{}
	}
	for action, keys := range preset {
		out[action] = slices.Clone(keys)
	}
	return out
}

// DefaultKeybindingsFor is the default keybindings under a preset: what
// fillMissingKeybinds fills a config from, and what a reset goes back to.
func DefaultKeybindingsFor(preset string) KeybindingsConfig {
	kb := DefaultConfig().Keybindings
	_ = applyPreset(&kb, preset)
	return kb
}

// CheatSheetLine is one line of a preset's cheat sheet: the keys to press and
// what they do.
type CheatSheetLine struct {
	Keys string `json:"keys"`
	Does string `json:"does"`
}

// CheatSheet is a preset in three groups: the keys that do what they do in the
// other multiplexer, the tuios keys the preset moved and where to, and what
// the other multiplexer has that tuios does not.
type CheatSheet struct {
	Preset  string           `json:"preset"`
	Model   string           `json:"model,omitempty"`
	Same    []CheatSheetLine `json:"same"`
	Moved   []CheatSheetLine `json:"moved"`
	Missing []string         `json:"missing"`
	Notes   []string         `json:"notes,omitempty"`
}

// PresetCheatSheet builds a preset's cheat sheet from its file and the keymap
// it gives, so the sheet cannot name a key the preset does not bind.
func PresetCheatSheet(name string) (CheatSheet, error) {
	p, err := loadPreset(name)
	if err != nil {
		return CheatSheet{}, err
	}
	kb := DefaultKeybindingsFor(name)
	leader := kb.LeaderKey
	describe := func(action string) string {
		if d := ActionDescriptions[action]; d != "" {
			return d
		}
		return action
	}
	sheet := CheatSheet{Preset: name, Model: p.About.Model, Missing: p.About.Missing, Notes: p.About.Notes}

	same := slices.Clone(p.About.Same)
	for action, keys := range p.PrefixMode {
		if len(keys) > 0 && action != "prefix_tuios" {
			same = append(same, action)
		}
	}
	slices.Sort(same)
	for _, action := range slices.Compact(same) {
		if keys := kb.PrefixMode[action]; len(keys) > 0 {
			sheet.Same = append(sheet.Same, CheatSheetLine{Keys: leader + " " + strings.Join(keys, " or "), Does: describe(action)})
		}
	}
	// A preset's window and workspace menu keys, behind the key that opens
	// the menu: ctrl+g p r under zellij.
	for _, sub := range []struct {
		gate    string
		preset  map[string][]string
		resolve map[string][]string
	}{
		{"prefix_window", p.WindowPrefix, kb.WindowPrefix},
		{"prefix_workspace", p.WorkspacePrefix, kb.WorkspacePrefix},
	} {
		gate := kb.PrefixMode[sub.gate]
		if len(gate) == 0 {
			continue
		}
		for _, action := range slices.Sorted(maps.Keys(sub.preset)) {
			if keys := sub.resolve[action]; len(keys) > 0 {
				sheet.Same = append(sheet.Same, CheatSheetLine{
					Keys: leader + " " + gate[0] + " " + strings.Join(keys, " or "),
					Does: describe(action),
				})
			}
		}
	}
	menu := leader + " " + strings.Join(kb.PrefixMode["prefix_tuios"], " or ")
	tuios := DefaultConfig().Keybindings.PrefixMode
	moved := slices.Sorted(maps.Keys(p.TuiosPrefix))
	for _, action := range moved {
		keys := kb.TuiosPrefix[action]
		if len(keys) == 0 {
			continue
		}
		line := CheatSheetLine{Keys: menu + " " + strings.Join(keys, " or "), Does: describe(action)}
		if was := tuios[action]; len(was) > 0 && !slices.Contains(keys, was[0]) {
			line.Does += " (was " + DefaultLeaderKey + " " + was[0] + ")"
		}
		sheet.Moved = append(sheet.Moved, line)
	}
	sheet.Same = collapseNumbered(sheet.Same)
	sheet.Moved = collapseNumbered(sheet.Moved)
	return sheet, nil
}

// collapseNumbered sorts lines by key and folds a run like "ctrl+b 1" to
// "ctrl+b 9", with the same words but the number, into one line.
func collapseNumbered(lines []CheatSheetLine) []CheatSheetLine {
	// sort.SliceStable rather than slices.SortStableFunc: a generic stable
	// sort is several KB of code per element type, for a list of fifty lines.
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].Keys < lines[j].Keys })
	digit := func(s string) (string, byte, bool) {
		if n := len(s); n > 0 && s[n-1] >= '0' && s[n-1] <= '9' && (n == 1 || s[n-2] == ' ') {
			return s[:n-1], s[n-1], true
		}
		return "", 0, false
	}
	var out []CheatSheetLine
	for i := 0; i < len(lines); {
		kp, kd, ok := digit(lines[i].Keys)
		dp, dd, ok2 := digit(lines[i].Does)
		j := i + 1
		for ok && ok2 && j < len(lines) {
			kp2, kd2, a := digit(lines[j].Keys)
			dp2, dd2, b := digit(lines[j].Does)
			if !a || !b || kp2 != kp || dp2 != dp || kd2 != dd2 {
				break
			}
			j++
		}
		if j-i >= 3 {
			last := lines[j-1]
			_, lk, _ := digit(last.Keys)
			out = append(out, CheatSheetLine{
				Keys: kp + string(kd) + " to " + string(lk),
				Does: dp + string(dd) + " to " + string(lk),
			})
		} else {
			out = append(out, lines[i:j]...)
		}
		i = j
	}
	return out
}

// The values of keybindings.leader_twice.
const (
	LeaderTwiceSend          = "send"
	LeaderTwiceLastWorkspace = "last_workspace"
)

// LeaderTwiceModes lists the values of keybindings.leader_twice.
var LeaderTwiceModes = []string{LeaderTwiceSend, LeaderTwiceLastWorkspace}
