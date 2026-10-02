package input

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/app"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
	"github.com/adrg/xdg"
)

func leader(o *app.OS, key tea.KeyPressMsg) *app.OS {
	o, _ = HandleKeyPress(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl}, o)
	o, _ = HandleKeyPress(key, o)
	return o
}

// legacyConfig loads src as the user's config.toml. The XDG search paths are
// resolved at package init, so the reload is what makes the redirect take.
func legacyConfig(t *testing.T, src string) *config.UserConfig {
	t.Helper()
	dir := t.TempDir()
	// Registered before t.Setenv so it runs after it: cleanups are LIFO, and a
	// reload that ran first would leave the xdg globals pointing at the temp
	// dir for the rest of the binary, after the directory is gone.
	t.Cleanup(xdg.Reload)
	t.Setenv("XDG_CONFIG_HOME", dir)
	xdg.Reload()
	if err := os.MkdirAll(filepath.Join(dir, "tuios"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tuios", "config.toml"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadUserConfig()
	if err != nil {
		t.Fatalf("LoadUserConfig: %v", err)
	}
	return cfg
}

// osWithFocusedPane builds an OS with one focused pane whose PTY records what
// reaches the guest, so a test can drive keys through the real HandleKeyPress
// path and see both what the app did and what the shell was typed into.
func osWithFocusedPane(t *testing.T, cfg *config.UserConfig, mode app.Mode) (*app.OS, *capturePty) {
	t.Helper()
	em := vt.NewEmulator(80, 24)
	t.Cleanup(func() { _ = em.Close() })
	pty := &capturePty{}
	win := &terminal.Window{ID: "prefix-0001", Terminal: em, Pty: pty, X: 0, Y: 0, Width: 82, Height: 26}
	o := app.NewOS(app.OSOptions{UserConfig: cfg, KeybindRegistry: config.NewKeybindRegistry(cfg)})
	// A screen big enough to hold the pane. Left at zero the dock band, which is
	// the bottom DockHeight rows of it, starts above row 0 and swallows every
	// mouse event before it can reach a pane. Keyboard tests do not notice, so
	// this only ever bit the first mouse test built on the helper.
	o.Width, o.Height = 120, 40
	o.Windows = []*terminal.Window{win}
	o.FocusedWindow = 0
	win.Workspace = o.CurrentWorkspace
	o.Mode = mode
	return o, pty
}
