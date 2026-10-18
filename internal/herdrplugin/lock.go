package herdrplugin

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
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
	// Ref is what the person asked for: a branch, a tag or a commit. ""
	// is the remote's default branch.
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

// LockPath is the lock file of d.
func (d Dirs) LockPath() string { return filepath.Join(d.TuiosConfig, LockName) }

// ManagedDir is the folder that holds the checkouts of installed plugins.
func (d Dirs) ManagedDir() string { return filepath.Join(d.TuiosData, "plugins") }

// CheckoutDir is the folder of e's checkout, the repository root. It is
// named by the plugin id and the first 12 digits of the commit, so two
// plugins from one repository never share a folder and an update can build
// the next commit beside the one that runs.
func (d Dirs) CheckoutDir(e LockEntry) string {
	return filepath.Join(d.ManagedDir(), PathComponent(e.ID), e.Commit[:min(12, len(e.Commit))])
}

// PluginRoot is the folder that holds e's manifest.
func (d Dirs) PluginRoot(e LockEntry) string {
	if e.Local() {
		return strings.TrimPrefix(e.Source, LocalPrefix)
	}
	return filepath.Join(d.CheckoutDir(e), filepath.FromSlash(e.Subdir))
}

// dataDir is tuios's data folder, the parent of worktrees/ and repos/.
func dataDir() string { return filepath.Join(xdg.DataHome, "tuios") }

// ExpandHome expands a leading ~ to the home folder.
func ExpandHome(p string) string { return expandHome(p) }

// Inside reports whether file is in dir once both have their links
// resolved. A manifest that is a link to a file elsewhere is not inside.
func Inside(file, dir string) bool {
	rel, err := filepath.Rel(canonical(dir), canonical(file))
	return err == nil && filepath.IsLocal(rel)
}

// checkManaged checks a loaded managed plugin against its lock entry: the
// id, that the manifest is in the checkout, and that the manifest is the
// one the person approved. A local folder is the person's, so only the id
// is checked.
func checkManaged(d Dirs, le LockEntry, p *Plugin) *Error {
	switch {
	case p.PluginID != le.ID:
		return errf("plugin_lock_mismatch", "the lock file installs %s, and the manifest in %s names %s. Install the plugin again", le.ID, p.PluginRoot, p.PluginID)
	case le.Local():
		return nil
	case !Inside(p.ManifestPath, d.CheckoutDir(le)):
		return errf("plugin_outside_checkout", "the manifest of %s is outside its checkout. Install the plugin again", le.ID)
	case TrustHash(le.Source, p, le.Grants) != le.Approved:
		return errf("plugin_changed", "the manifest of %s is different from the approved one in plugins.lock. Install the plugin again", le.ID)
	}
	return nil
}

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

// lockWait is how long Exclusive waits for another command to finish.
const lockWait = 10 * time.Second

// Exclusive creates p with O_EXCL as a guard, waiting up to 10 seconds for
// another tuios plugins command to remove it. The file is open for writing.
func Exclusive(p string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return nil, err
	}
	deadline := time.Now().Add(lockWait)
	for {
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // a guard file tuios names
		if !errors.Is(err, os.ErrExist) {
			return f, err
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("another tuios plugins command holds %s. Wait for it to finish. If none runs, remove the file", p)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// UpdateLock reads the lock file at p, lets change edit it, and writes it
// back. The temporary file is also the guard: a second install waits for
// the first one to rename it into place.
func UpdateLock(p string, change func(*Lock)) error {
	tmp := p + ".tmp"
	f, err := Exclusive(tmp)
	if err != nil {
		return err
	}
	l, err := ReadLock(p)
	if err == nil {
		change(l)
		l.Version = lockVersion
		var data []byte
		if data, err = toml.Marshal(l); err == nil {
			_, err = f.Write(data)
		}
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, p)
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
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
	if err := CheckSource(e.Source); err != nil {
		return err
	}
	if err := CheckSubdir(e.Subdir); err != nil {
		return err
	}
	return CheckRef(e.Ref)
}

// IsCommit reports whether s is a full 40-digit commit id in lower case.
func IsCommit(s string) bool {
	if len(s) != 40 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil && strings.ToLower(s) == s
}

// hasControl reports whether s holds a character that can move the cursor
// or change what a terminal shows: C0 except tab, DEL, C1, and the bidi
// controls.
func hasControl(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool {
		return r < 0x20 && r != '\t' || r >= 0x7f && r < 0xa0 || r == 0x200e || r == 0x200f || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069
	})
}

// CheckSource refuses a git source that git could read as an option or as
// a transport helper (transport::address), or that holds a control
// character.
func CheckSource(s string) error {
	if strings.HasPrefix(strings.ToLower(s), "http://") {
		return errors.New("tuios does not get plugins over plain http. Use https")
	}
	if s == "" || s[0] == '-' || strings.Contains(s, "::") || hasControl(s) {
		return fmt.Errorf("%q is not a valid plugin source", s)
	}
	return nil
}

// CheckSubdir refuses a plugin folder that leaves the repository: an
// absolute path, a .. part, a backslash or a control character.
func CheckSubdir(s string) error {
	if s != "" && (!filepath.IsLocal(s) || path.Clean(s) != s || s[0] == '-' || strings.ContainsAny(s, `\:`) || hasControl(s)) {
		return errors.New("the plugin folder must be a relative path inside the repository, with no .. parts")
	}
	return nil
}

// CheckRef refuses a ref git could read as an option or that holds a
// character no ref can hold.
func CheckRef(s string) error {
	if s != "" && (s[0] == '-' || strings.Contains(s, "..") || strings.Contains(s, "@{") || strings.ContainsAny(s, " ~^:?*[\\") || hasControl(s)) {
		return fmt.Errorf("%q is not a valid git ref", s)
	}
	return nil
}

// TrustHash is sha256 over what p runs and asks for, with source: each
// field ends in NUL and each section starts with a tag byte. The lock file
// keeps it as approved, so a changed manifest can be told from a changed
// program.
func TrustHash(source string, p *Plugin, grants []string) string {
	h := sha256.New()
	put := func(tag byte, fields ...string) {
		h.Write([]byte{tag})
		for _, f := range fields {
			_, _ = io.WriteString(h, f)
			h.Write([]byte{0})
		}
	}
	put('s', source)
	put('g', grants...)
	put('P', p.Platforms...)
	for _, b := range p.Build {
		put('b', b.Command...)
		put('P', b.Platforms...)
	}
	for _, s := range p.Startup {
		put('u', s.Command...)
		put('P', s.Platforms...)
	}
	for _, e := range p.Events {
		put('e', append([]string{e.On}, e.Command...)...)
		put('P', e.Platforms...)
	}
	for _, a := range p.Actions {
		put('a', append([]string{a.ID}, a.Command...)...)
		put('P', a.Platforms...)
	}
	for _, x := range p.Panes {
		put('p', append([]string{x.ID, x.Placement}, x.Command...)...)
		put('P', x.Platforms...)
	}
	for _, l := range p.LinkHandlers {
		put('l', l.ID, l.Pattern, l.Action)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

func dirExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
