package tutor

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/adrg/xdg"

	"github.com/Gaurav-Gosain/tuios/internal/sound"
)

// sandbox is the temp dir the tutor points the home and every XDG folder at
// for the rest of the process. Nothing the tutor runs has a reason to write
// a file, and anything that writes one anyway lands here and goes with it.
type sandbox struct {
	dir  string
	once sync.Once
}

// sandboxDirs are the variables the tutor points inside the sandbox.
// PATH names an empty folder, so a stray exec of a program by name finds
// nothing to run.
var sandboxDirs = []struct{ key, sub string }{
	{"HOME", "home"},
	{"XDG_CONFIG_HOME", "config"},
	{"XDG_STATE_HOME", "state"},
	{"XDG_CACHE_HOME", "cache"},
	{"XDG_DATA_HOME", "data"},
	{"XDG_RUNTIME_DIR", "run"},
	{"XDG_CONFIG_DIRS", "config-dirs"},
	{"XDG_DATA_DIRS", "data-dirs"},
	{"TMPDIR", "tmp"},
	{"PATH", "bin"},
}

// enterSandbox makes the temp dir and moves the process into it: the home,
// the XDG folders, TMPDIR and PATH point inside it, and every TUIOS_
// variable is gone, so nothing can name the person's daemon, server or
// pane. It returns the sandbox, whose remove takes the dir away again.
func enterSandbox() (*sandbox, error) {
	dir, err := os.MkdirTemp("", "tuios-tutor-")
	if err != nil {
		return nil, err
	}
	s := &sandbox{dir: dir}
	for _, kv := range os.Environ() {
		if name, _, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(name, "TUIOS_") {
			_ = os.Unsetenv(name)
		}
	}
	for _, d := range sandboxDirs {
		path := filepath.Join(dir, d.sub)
		if err := os.Mkdir(path, 0o700); err != nil {
			s.remove()
			return nil, err
		}
		_ = os.Setenv(d.key, path)
	}
	if runtime.GOOS == "windows" {
		_ = os.Setenv("USERPROFILE", filepath.Join(dir, "home"))
		_ = os.Setenv("APPDATA", filepath.Join(dir, "config"))
		_ = os.Setenv("LOCALAPPDATA", filepath.Join(dir, "cache"))
	}
	// No sound: the cue player is a process of its own.
	_ = os.Setenv(sound.DisableEnv, "1")
	// The xdg package read the folders at init.
	xdg.Reload()
	return s, nil
}

// remove takes the sandbox away. It is safe to call more than once and from
// any goroutine: the program's end and a signal can both call it.
func (s *sandbox) remove() {
	if s == nil {
		return
	}
	s.once.Do(func() { _ = os.RemoveAll(s.dir) })
}
