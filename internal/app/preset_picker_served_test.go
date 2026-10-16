package app

import (
	"slices"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestServedPresetPickerDoesNotWrite is a boundary: the keybind preset is
// written to config.toml, which on a tuios ssh or tuios-web server is the
// operator's file. A served client gets no welcome offer and cannot apply a
// preset. The local row is the positive half.
//
// Negative control: with the ConfigReadOnly check removed from
// PresetPickerApply, the ssh row sets the preset.
func TestServedPresetPickerDoesNotWrite(t *testing.T) {
	for _, tc := range []struct {
		kind   ClientKind
		writes bool
	}{
		{ClientSSH, false},
		{ClientLocal, true},
	} {
		t.Run(tc.kind.String(), func(t *testing.T) {
			m := &OS{Settings: config.Global, Width: 120, Height: 40, Client: tc.kind, ConfigReadOnly: tc.kind.Remote()}
			m.UserConfig = config.DefaultConfig()
			if tc.kind.Remote() && m.welcomePresetChip().Key != "" {
				t.Error("a served client was offered a preset from the operator's home folder")
			}
			m.OpenKeybindPresets()
			m.presetPicker.selected = slices.Index(config.Presets(), "tmux")
			cmd := m.PresetPickerApply()
			if wrote := cmd != nil || m.UserConfig.Keybindings.Preset == "tmux"; wrote != tc.writes {
				t.Errorf("applying tmux: wrote = %v, want %v", wrote, tc.writes)
			}
		})
	}
}
