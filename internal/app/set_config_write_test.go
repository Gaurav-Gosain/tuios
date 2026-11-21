package app

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestAReadOnlyClientWritesNothing keeps the served-session contract: a client
// told its config is not its own applies the change for as long as it lasts and
// leaves the file to whoever owns it.
//
// Negative control: making persistSettings ignore ConfigReadOnly wrote the
// change and this failed.
func TestAReadOnlyClientWritesNothing(t *testing.T) {
	file := useTempConfig(t)
	if err := config.WriteConfigFile(config.DefaultConfig(), file); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	before, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read seeded config: %v", err)
	}

	m := NewOS(OSOptions{UserConfig: config.DefaultConfig(), ConfigReadOnly: true})
	m.RemoteCommandChan = make(chan RemoteCommandMsg, 1)
	_, cmd := m.Update(RemoteCommandMsg{
		CommandType: "set_config",
		ConfigPath:  "appearance.border_style",
		ConfigValue: "thick",
	})
	close(m.RemoteCommandChan)
	drainCommand(t, cmd)

	after, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(after) != string(before) {
		t.Error("a read-only client rewrote the config file")
	}
	if m.UserConfig.Appearance.BorderStyle != "thick" {
		t.Errorf("the change did not apply live either: %q", m.UserConfig.Appearance.BorderStyle)
	}
}

func drainCommand(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		return
	}
	for _, c := range batch {
		if c != nil {
			drainCommand(t, c)
		}
	}
}

// TestARoutedSetConfigReachesTheFile drives the same message the CLI's verb
// routes and then reads the file back.
//
// Negative control: removing the m.persistSettings() call from the set_config
// arm left config.toml holding the seeded defaults, so neither the screenshot
// key nor the appearance key was found and both assertions failed. Confirmed
// against a tree with that line taken out.
func TestARoutedSetConfigReachesTheFile(t *testing.T) {
	for _, tc := range []struct {
		name, path, value, want string
	}{
		{"screenshot", "screenshot.font_family", "JetBrainsMono Nerd Font Mono",
			`font_family = "JetBrainsMono Nerd Font Mono"`},
		{"appearance", "appearance.border_style", "thick", `border_style = "thick"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := useTempConfig(t)
			if err := config.WriteConfigFile(config.DefaultConfig(), file); err != nil {
				t.Fatalf("seed config: %v", err)
			}
			m := NewOS(OSOptions{UserConfig: config.DefaultConfig()})
			m.RemoteCommandChan = make(chan RemoteCommandMsg, 1)

			model, cmd := m.Update(RemoteCommandMsg{
				CommandType: "set_config",
				ConfigPath:  tc.path,
				ConfigValue: tc.value,
			})
			if _, ok := model.(*OS); !ok {
				t.Fatalf("Update returned a %T", model)
			}
			if cmd == nil {
				t.Fatal("the routed change handed back no command at all")
			}
			close(m.RemoteCommandChan)
			drainCommand(t, cmd)

			written, err := os.ReadFile(file)
			if err != nil {
				t.Fatalf("read config: %v", err)
			}
			if !strings.Contains(string(written), tc.want) {
				t.Errorf("set-config %s did not survive to the file; %s is missing from:\n%s",
					tc.path, tc.want, written)
			}
		})
	}
}
