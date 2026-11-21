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

type sandbox struct {
	dir  string
	once sync.Once
}

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
	_ = os.Setenv(sound.DisableEnv, "1")
	xdg.Reload()
	return s, nil
}

func (s *sandbox) remove() {
	if s == nil {
		return
	}
	s.once.Do(func() { _ = os.RemoveAll(s.dir) })
}
