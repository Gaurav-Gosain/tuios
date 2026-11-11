package fuzz

import (
	"os"
	"os/exec"
	"path/filepath"

	"github.com/adrg/xdg"
)

// Sandbox fences a model under fuzzing off from the machine it runs on. With
// keys and the mouse routed through the real input handler, an action can do
// whatever a person can: open a pane, open a link or an editor, record a
// tape, save a layout or the config. Sandbox makes each of those land
// nowhere that matters:
//
//   - SHELL is true, so a pane the model opens without a daemon exits at once.
//   - PATH is an empty folder, so a link, an editor or a notifier the model
//     starts is not found.
//   - The XDG data and config roots are folders under dir, so a tape, a
//     layout or a saved config is written there.
//
// The model's own config should be read-only as well (OSOptions.ConfigReadOnly).
// restore puts everything back; call it when the target closes.
func Sandbox(dir string) (restore func(), err error) {
	bin := filepath.Join(dir, "empty-path")
	if err := os.MkdirAll(bin, 0o750); err != nil {
		return nil, err
	}
	shell, err := exec.LookPath("true")
	if err != nil {
		return nil, err
	}
	prevShell, hadShell := os.LookupEnv("SHELL")
	prevPath, hadPath := os.LookupEnv("PATH")
	prevData, prevConfig := xdg.DataHome, xdg.ConfigHome
	if err := os.Setenv("SHELL", shell); err != nil {
		return nil, err
	}
	if err := os.Setenv("PATH", bin); err != nil {
		return nil, err
	}
	xdg.DataHome = filepath.Join(dir, "data")
	xdg.ConfigHome = filepath.Join(dir, "config")
	return func() {
		putEnv("SHELL", prevShell, hadShell)
		putEnv("PATH", prevPath, hadPath)
		xdg.DataHome, xdg.ConfigHome = prevData, prevConfig
	}, nil
}

func putEnv(key, value string, had bool) {
	if had {
		_ = os.Setenv(key, value)
		return
	}
	_ = os.Unsetenv(key)
}
