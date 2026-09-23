package tmuxcompat

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// FindRealTmux finds the tmux a call not meant for the shim goes to: the first
// `tmux` on pathEnv that is not the shim's own link and does not resolve to
// self, the tuios binary.
func FindRealTmux(pathEnv, dir, self string) (string, error) {
	name := "tmux"
	if runtime.GOOS == "windows" {
		name = "tmux.exe"
	}
	selfReal := self
	if r, err := filepath.EvalSymlinks(self); err == nil {
		selfReal = r
	}
	skip := ""
	if dir != "" {
		skip = filepath.Clean(BinDir(dir))
	}
	for _, d := range filepath.SplitList(pathEnv) {
		if d == "" || filepath.Clean(d) == skip {
			continue
		}
		p := filepath.Join(d, name)
		st, err := os.Stat(p)
		if err != nil || st.IsDir() || st.Mode()&0o111 == 0 && runtime.GOOS != "windows" {
			continue
		}
		if r, err := filepath.EvalSymlinks(p); err == nil && r == selfReal {
			continue
		}
		return p, nil
	}
	return "", errors.New("no tmux on PATH other than the tuios shim")
}
