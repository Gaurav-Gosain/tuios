package herdrplugin

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// The state file is the machine's half of an install. plugins.lock says
// which commit a plugin is pinned to, and can be shared through dotfiles.
// The state file, in the data folder beside the build folders, says which
// build folder runs that commit on this machine.
//
// The names below are tuios's own files in the managed folder. A plugin
// folder is PathComponent of the id, which writes % only before two upper
// case hex digits (or as %plugin, %. and %..). A % followed by a lower case
// letter never comes out of it, so these names never meet a plugin folder.
const (
	// StateName is the state file in the managed folder.
	StateName = "%state.toml"
	// InstallLockName is the install lock in the managed folder.
	InstallLockName = "%install.lock"
	// FetchPrefix starts a fetch folder in the managed folder.
	FetchPrefix = "%fetch-"
)

// StateEntry is where one plugin is built on this machine.
type StateEntry struct {
	ID string `toml:"id"`
	// Commit is the commit built in Dir.
	Commit string `toml:"commit"`
	// Dir is the build folder, relative to the tuios data folder:
	// plugins/<id>/<commit12>-<nonce>.
	Dir   string `toml:"dir"`
	Built bool   `toml:"built"`
	// PrevCommit and PrevDir are the build before it. The plugin runs from
	// PrevDir while plugins.lock still names PrevCommit, which is the case
	// after an install that was killed between the state and lock writes.
	PrevCommit string `toml:"prev_commit,omitempty"`
	PrevDir    string `toml:"prev_dir,omitempty"`
}

// State is the state file.
type State struct {
	Plugins []StateEntry `toml:"plugin"`
}

// StatePath is the state file of d.
func (d Dirs) StatePath() string { return filepath.Join(d.ManagedDir(), StateName) }

// Find is the entry for id, or nil.
func (s *State) Find(id string) *StateEntry {
	for i := range s.Plugins {
		if s.Plugins[i].ID == id {
			return &s.Plugins[i]
		}
	}
	return nil
}

// Live is the build folder that runs le: the state's dir when its commit is
// the lock's, else the previous dir when that commit is. ok is false when
// neither matches, or the dir is not a folder of le.
func (s *State) Live(le LockEntry) (dir string, built, ok bool) {
	st := s.Find(le.ID)
	switch {
	case st == nil:
		return "", false, false
	case st.Commit == le.Commit:
		dir, built = st.Dir, st.Built
	case st.PrevCommit == le.Commit && st.PrevDir != "":
		dir, built = st.PrevDir, true
	default:
		return "", false, false
	}
	return dir, built, CheckDir(le.ID, dir) == nil
}

// CheckDir refuses a state dir that is not one folder in plugins/<id>/, so
// the state file cannot point a plugin at a folder elsewhere.
func CheckDir(id, dir string) error {
	rest, ok := strings.CutPrefix(dir, "plugins/"+PathComponent(id)+"/")
	if !ok || rest == "" || rest == "." || rest == ".." || strings.ContainsAny(rest, `/\:`) || hasControl(rest) {
		return fmt.Errorf("the state of %s names an invalid folder %q", id, dir)
	}
	return nil
}

// ReadState reads the state file at p. A missing file is an empty state.
func ReadState(p string) (*State, error) {
	var s State
	data, err := os.ReadFile(p) //nolint:gosec // the state file in the data folder
	if errors.Is(err, os.ErrNotExist) {
		return &s, nil
	}
	if err == nil {
		err = toml.Unmarshal(data, &s)
	}
	if err != nil {
		return nil, fmt.Errorf("%s cannot be read: %w", p, err)
	}
	return &s, nil
}

// WriteState writes the state file at p with WriteAtomic. The caller holds
// the install lock.
func WriteState(p string, s *State) error {
	data, err := toml.Marshal(s)
	if err != nil {
		return err
	}
	return WriteAtomic(p, data)
}

// WriteAtomic writes data to p through a new temporary file in the same
// folder: written, synced to disk, then renamed over p, and the folder
// synced on unix. A reader sees the old file or the new one, also after a
// power loss.
func WriteAtomic(p string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(p), "%tmp-*")
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = Rename(f.Name(), p)
	}
	if err != nil {
		_ = os.Remove(f.Name())
		return err
	}
	if runtime.GOOS != "windows" {
		if dir, err := os.Open(filepath.Dir(p)); err == nil {
			_ = dir.Sync()
			_ = dir.Close()
		}
	}
	return nil
}

// Rename is os.Rename. On Windows it tries again for about two seconds,
// because a virus scanner or the indexer can hold a new file open for a
// moment.
func Rename(from, to string) error {
	err := os.Rename(from, to)
	for i := 0; err != nil && runtime.GOOS == "windows" && i < 10; i++ {
		time.Sleep(200 * time.Millisecond)
		err = os.Rename(from, to)
	}
	return err
}
