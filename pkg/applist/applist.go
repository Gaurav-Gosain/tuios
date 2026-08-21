// Package applist enumerates the programs a launcher can run.
//
// The one source it implements is $PATH, because that is the set a shell would
// run and a launcher that disagrees with the shell is lying about what typing a
// name will do. Entry is deliberately source-agnostic so a caller with another
// source, such as a GUI launcher reading .desktop files, can merge its own
// entries into the same list and rank them with the same matcher.
package applist

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// SourcePath names the $PATH source on an Entry.
//
// Source exists because there is more than one place a launchable thing can
// come from, and one of them is a decision worth writing down: a .desktop entry
// with Terminal=true declares that it must run inside a terminal emulator.
// tuios is one, so those entries are things tuios can run natively and a GUI
// launcher cannot without spawning a terminal first. They are still not
// surfaced in the tuios palette, for three reasons.
//
// Almost every such entry's Exec is a program already on $PATH, so listing both
// puts the same program in the list twice under two names. The palette's whole
// job is that the right row is first, and near-synonyms work directly against
// that; the cost is paid on every query while the benefit reaches only the
// handful of entries that carry arguments or a name the basename does not
// reveal.
//
// A correct reading of a .desktop file is not small: Exec field codes, TryExec,
// NoDisplay, Hidden, OnlyShowIn, localised Name keys and Actions. Half of that
// produces rows that fail when activated, which is worse than a row that is not
// there.
//
// And it is Linux and BSD only. $PATH means the same thing on every platform
// tuios builds for, so the palette's contents stay explicable; desktop entries
// would make them depend on the host.
//
// The seam is here rather than the parser because the GUI launcher has to parse
// .desktop files anyway, having no other source. When it does, it can hand
// Entry values back with its own Source and tuios can merge them without a
// second parser existing. The rule that would make that worth doing: admit a
// Terminal=true entry only when its Exec does not resolve to a $PATH entry of
// the same name, so it is additive rather than a synonym.
const SourcePath = "path"

// Entry is one launchable program.
type Entry struct {
	// Name is what the user types and what a launcher displays: the basename
	// for a $PATH entry.
	Name string
	// Path is the absolute path to execute.
	Path string
	// Dir is the $PATH entry the program was found in, which is the only way to
	// tell a shadowed name apart from the one that won.
	Dir string
	// Source names where the entry came from, so a merged list can say. Empty
	// means SourcePath.
	Source string
}

// Dirs returns the $PATH entries in order.
//
// Relative entries are dropped rather than resolved. A shell resolves them
// against its own working directory, which a launcher does not have, and
// running whatever happens to sit in the current directory is how a launcher
// becomes an attack.
func Dirs() []string {
	return splitPath(os.Getenv("PATH"))
}

func splitPath(path string) []string {
	if path == "" {
		return nil
	}
	raw := strings.Split(path, string(os.PathListSeparator))
	dirs := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, d := range raw {
		if d == "" || !filepath.IsAbs(d) {
			continue
		}
		d = filepath.Clean(d)
		if _, dup := seen[d]; dup {
			// A duplicated $PATH entry can only ever be shadowed by its first
			// appearance, so scanning it twice is pure cost.
			continue
		}
		seen[d] = struct{}{}
		dirs = append(dirs, d)
	}
	return dirs
}

// Scan reads dirs in order and returns the executables in them, deduplicated by
// name with the earliest directory winning, which is the rule a shell resolves
// by.
//
// Scan does filesystem I/O proportional to the size of every directory. Call it
// off any goroutine that must stay responsive.
func Scan(dirs []string) []Entry {
	var out []Entry
	seen := make(map[string]struct{}, 1024)
	for _, dir := range dirs {
		out = merge(out, seen, readDir(dir))
	}
	return out
}

func merge(out []Entry, seen map[string]struct{}, entries []Entry) []Entry {
	for _, e := range entries {
		if _, dup := seen[e.Name]; dup {
			continue
		}
		seen[e.Name] = struct{}{}
		out = append(out, e)
	}
	return out
}

// readDir returns the executables directly in dir, in name order, before
// deduplication against earlier directories.
func readDir(dir string) []Entry {
	items, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make([]Entry, 0, len(items))
	for _, item := range items {
		if item.IsDir() {
			continue
		}
		path := filepath.Join(dir, item.Name())
		info, err := item.Info()
		if err != nil {
			continue
		}
		if info.Mode()&fs.ModeSymlink != 0 {
			// A symlink's own mode says nothing, and a dangling one must not be
			// offered, so the target is what gets asked.
			if info, err = os.Stat(path); err != nil {
				continue
			}
		}
		if !info.Mode().IsRegular() || !executable(info) {
			continue
		}
		out = append(out, Entry{Name: item.Name(), Path: path, Dir: dir, Source: SourcePath})
	}
	return out
}

// Cache holds a scan and redoes only the part of it that changed.
//
// Reopening a launcher restats one directory per $PATH entry and reuses
// everything whose mtime has not moved, so the second open is effectively free
// while a program installed since the first still shows up. A Cache is safe for
// concurrent use.
type Cache struct {
	mu      sync.Mutex
	dirs    map[string]cachedDir
	entries []Entry
}

type cachedDir struct {
	mtime   time.Time
	entries []Entry
}

// NewCache returns an empty cache. Its first Refresh is a full scan.
func NewCache() *Cache {
	return &Cache{dirs: make(map[string]cachedDir)}
}

// Entries returns the most recent scan without touching the filesystem, so it
// is safe to call from a render or input path. It is nil before the first
// Refresh.
func (c *Cache) Entries() []Entry {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.entries
}

// Refresh rescans the directories whose mtime moved and reports the current
// list along with whether anything changed. It does filesystem I/O; call it off
// the goroutine that has to stay responsive.
func (c *Cache) Refresh() ([]Entry, bool) {
	return c.refresh(Dirs())
}

func (c *Cache) refresh(dirs []string) ([]Entry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	fresh := make(map[string]cachedDir, len(dirs))
	changed := len(dirs) != len(c.dirs)
	var out []Entry
	seen := make(map[string]struct{}, 1024)

	for _, dir := range dirs {
		var mtime time.Time
		if info, err := os.Stat(dir); err == nil {
			mtime = info.ModTime()
		}
		prev, had := c.dirs[dir]
		// A zero mtime means the directory could not be stat'd, and reusing a
		// cached listing for a directory that has gone away would keep offering
		// programs that are no longer there.
		if had && !mtime.IsZero() && prev.mtime.Equal(mtime) {
			fresh[dir] = prev
			out = merge(out, seen, prev.entries)
			continue
		}
		changed = true
		listed := readDir(dir)
		fresh[dir] = cachedDir{mtime: mtime, entries: listed}
		out = merge(out, seen, listed)
	}

	c.dirs = fresh
	if !changed {
		return c.entries, false
	}
	c.entries = out
	return out, true
}
