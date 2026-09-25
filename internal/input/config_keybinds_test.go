package input

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
)

func press(key string) tea.KeyPressMsg {
	switch key {
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace}
	}
	runes := []rune(key)
	return tea.KeyPressMsg{Code: runes[0], Text: key}
}

// osWithBindings builds an OS whose keybind registry comes from the default
// config with one section overridden, which is what a user editing config.toml
// ends up with after the defaults are filled in.
func osWithBindings(t *testing.T, override func(*config.KeybindingsConfig)) *app.OS {
	t.Helper()
	cfg := config.DefaultConfig()
	override(&cfg.Keybindings)
	return app.NewOS(app.OSOptions{
		UserConfig:      cfg,
		KeybindRegistry: config.NewKeybindRegistry(cfg),
	})
}
