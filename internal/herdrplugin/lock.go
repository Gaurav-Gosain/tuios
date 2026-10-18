package herdrplugin

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/adrg/xdg"
	"github.com/pelletier/go-toml/v2"
)

// The lock file pins the plugins tuios plugins install put on this machine.
// It lives beside config.toml, so a person who keeps the config folder in
// dotfiles gets the same commits on the next machine. The checkouts live in
// the data folder, one folder per plugin and commit, because a build fills
// them with node_modules and target folders that do not belong in dotfiles.
//
// The lock file never enables a plugin. [plugins] enabled in config.toml
// stays the only switch, and the daemon only reads the lock file and the
// checkouts: it never fetches and never builds.

// LockName is the file name of the lock file in the config folder.
const LockName = "plugins.lock"

// lockVersion is the version of the lock file format this build writes.
const lockVersion = 1

// LocalPrefix starts the source of a plugin installed from a local folder.
const LocalPrefix = "path:"

// LockEntry is one [[plugin]] of the lock file.
type LockEntry struct {
	ID     string `toml:"id" json:"id"`
	Format string `toml:"format" json:"format"`
	// Source is what was installed: a git URL, or LocalPrefix and an
	// absolute folder.
	Source string `toml:"source" json:"source"`
	// Subdir is the plugin's folder inside the repository, "" for the root.
	Subdir string `toml:"subdir,omitempty" json:"subdir,omitempty"`
	// Ref is what the person asked for: a branch, a tag or a commit.
	Ref string `toml:"ref,omitempty" json:"ref,omitempty"`
	// Commit is the full commit the ref resolved to. It is the pin.
	Commit string `toml:"commit,omitempty" json:"commit,omitempty"`
	// Tree is git's id of the plugin folder at Commit.
	Tree string `toml:"tree,omitempty" json:"tree,omitempty"`
	// Approved is TrustHash of the manifest the person said yes to.
	Approved string `toml:"approved" json:"approved"`
	// Grants are the pane grants a native plugin asked for. A herdr plugin
	// gets the default grants and leaves this empty.
	Grants    []string  `toml:"grants,omitempty" json:"grants,omitempty"`
	Installed time.Time `toml:"installed" json:"installed"`
	Built     bool      `toml:"built" json:"built"`
}

// Local reports whether the entry is a folder the person owns, installed in
// place.
func (e LockEntry) Local() bool { return strings.HasPrefix(e.Source, LocalPrefix) }

// Lock is the lock file.
type Lock struct {
	Version int         `toml:"version"`
	Plugins []LockEntry `toml:"plugin"`
}

// Find is the entry for id, or nil.
func (l *Lock) Find(id string) *LockEntry {
	for i := range l.Plugins {
		if l.Plugins[i].ID == id {
			return &l.Plugins[i]
		}
	}
	return nil
}

// Put adds e, or replaces the entry with its id.
func (l *Lock) Put(e LockEntry) {
	if old := l.Find(e.ID); old != nil {
		*old = e
		return
	}
	l.Plugins = append(l.Plugins, e)
}

// LockPath is the lock file of d.
func (d Dirs) LockPath() string { return filepath.Join(d.TuiosConfig, LockName) }

// ManagedDir is the folder that holds the checkouts of installed plugins.
func (d Dirs) ManagedDir() string { return filepath.Join(d.TuiosData, "plugins") }

// CheckoutDir is the folder of e's checkout, the repository root. It is
// named by the plugin id and the first 12 digits of the commit, so two
// plugins from one repository never share a folder and an update can build
// the next commit beside the one that runs.
func (d Dirs) CheckoutDir(e LockEntry) string {
	c := e.Commit
	if len(c) > 12 {
		c = c[:12]
	}
	return filepath.Join(d.ManagedDir(), PathComponent(e.ID), c)
}

// PluginRoot is the folder that holds e's manifest.
func (d Dirs) PluginRoot(e LockEntry) string {
	if e.Local() {
		return strings.TrimPrefix(e.Source, LocalPrefix)
	}
	if e.Subdir == "" {
		return d.CheckoutDir(e)
	}
	return filepath.Join(d.CheckoutDir(e), filepath.FromSlash(e.Subdir))
}

// dataDir is tuios's data folder, the parent of worktrees/ and repos/.
func dataDir() string { return filepath.Join(xdg.DataHome, "tuios") }

// ReadLock reads the lock file at p. A missing file is an empty lock.
func ReadLock(p string) (*Lock, error) {
	data, err := os.ReadFile(p) //nolint:gosec // the lock file in the person's config folder
	if errors.Is(err, os.ErrNotExist) {
		return &Lock{Version: lockVersion}, nil
	}
	if err != nil {
		return nil, err
	}
	var l Lock
	if err := toml.Unmarshal(data, &l); err != nil {
		return nil, fmt.Errorf("%s is not a valid lock file: %w", p, err)
	}
	if l.Version > lockVersion {
		return nil, fmt.Errorf("%s has version %d. This tuios reads version %d. Update tuios", p, l.Version, lockVersion)
	}
	return &l, nil
}

// lockWait is how long UpdateLock waits for another install to finish.
const lockWait = 10 * time.Second

// UpdateLock reads the lock file at p, lets change edit it, and writes it
// back. The temporary file, created with O_EXCL, is also the guard: a
// second install waits for the first one to rename it into place.
func UpdateLock(p string, change func(*Lock) error) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp := p + ".tmp"
	deadline := time.Now().Add(lockWait)
	var f *os.File
	for {
		var err error
		f, err = os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // beside the lock file
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("another tuios plugins command holds %s. Wait for it to finish. If none runs, remove the file", tmp)
		}
		time.Sleep(100 * time.Millisecond)
	}
	done := false
	defer func() {
		if !done {
			_ = f.Close()
			_ = os.Remove(tmp)
		}
	}()
	l, err := ReadLock(p)
	if err != nil {
		return err
	}
	if err := change(l); err != nil {
		return err
	}
	l.Version = lockVersion
	data, err := toml.Marshal(l)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		return err
	}
	done = true
	return nil
}

// CheckLockEntry checks the fields of e that name a path or reach git: a
// lock file can come from a dotfiles repository the person did not write.
func CheckLockEntry(e LockEntry) error {
	if n, ok := NormalizeID(e.ID); !ok || n != e.ID {
		return fmt.Errorf("the lock file has an invalid plugin id %q", e.ID)
	}
	if e.Local() {
		if !filepath.IsAbs(strings.TrimPrefix(e.Source, LocalPrefix)) {
			return fmt.Errorf("the lock file entry %s names a folder that is not absolute", e.ID)
		}
		return nil
	}
	if !IsCommit(e.Commit) {
		return fmt.Errorf("the lock file entry %s has no full commit", e.ID)
	}
	if err := CheckSubdir(e.Subdir); err != nil {
		return fmt.Errorf("the lock file entry %s: %w", e.ID, err)
	}
	if err := CheckRef(e.Ref); err != nil {
		return fmt.Errorf("the lock file entry %s: %w", e.ID, err)
	}
	if e.Source == "" || strings.HasPrefix(e.Source, "-") || strings.ContainsAny(e.Source, "\x00\n\r") {
		return fmt.Errorf("the lock file entry %s has an invalid source", e.ID)
	}
	return nil
}

// IsCommit reports whether s is a full 40-digit commit id in lower case.
func IsCommit(s string) bool {
	if len(s) != 40 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil && strings.ToLower(s) == s
}

// CheckSubdir refuses a plugin folder that leaves the repository: an
// absolute path, a .. part, a backslash or a control character.
func CheckSubdir(s string) error {
	if s == "" {
		return nil
	}
	bad := errors.New("the plugin folder must be a relative path inside the repository, with no .. parts")
	if strings.ContainsAny(s, "\\:\x00") || strings.HasPrefix(s, "/") || strings.HasPrefix(s, "-") {
		return bad
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return bad
		}
	}
	if path.Clean(s) != s {
		return bad
	}
	for part := range strings.SplitSeq(s, "/") {
		if part == ".." || part == "." {
			return bad
		}
	}
	return nil
}

// CheckRef refuses a ref git could read as an option or that holds a
// character no ref can hold.
func CheckRef(s string) error {
	if s == "" {
		return nil
	}
	if strings.HasPrefix(s, "-") || strings.Contains(s, "..") || strings.ContainsAny(s, " ~^:?*[\\\x00") {
		return fmt.Errorf("%q is not a valid git ref", s)
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%q is not a valid git ref", s)
		}
	}
	return nil
}

// trustSurface is what a plugin runs on its own or asks for: the part of a
// manifest a person approves. TrustHash hashes it.
type trustSurface struct {
	Source  string     `json:"source"`
	Build   [][]string `json:"build"`
	Startup [][]string `json:"startup"`
	Events  [][]string `json:"events"`
	Actions [][]string `json:"actions"`
	Panes   [][]string `json:"panes"`
	Links   [][]string `json:"link_handlers"`
	Grants  []string   `json:"grants"`
}

// TrustHash is sha256 over a canonical JSON of what p runs and asks for,
// with source. The lock file keeps it as approved, so an update can tell a
// change of code from a change of what the plugin may do.
func TrustHash(source string, p *Plugin, grants []string) string {
	t := trustSurface{Source: source, Grants: grants}
	for _, b := range p.Build {
		t.Build = append(t.Build, b.Command)
	}
	for _, s := range p.Startup {
		t.Startup = append(t.Startup, s.Command)
	}
	for _, e := range p.Events {
		t.Events = append(t.Events, append([]string{e.On}, e.Command...))
	}
	for _, a := range p.Actions {
		t.Actions = append(t.Actions, append([]string{a.ID}, a.Command...))
	}
	for _, x := range p.Panes {
		t.Panes = append(t.Panes, append([]string{x.ID, x.Placement}, x.Command...))
	}
	for _, h := range p.LinkHandlers {
		t.Links = append(t.Links, []string{h.ID, h.Pattern, h.Action})
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(t)
	sum := sha256.Sum256(buf.Bytes())
	return "sha256:" + hex.EncodeToString(sum[:])
}
