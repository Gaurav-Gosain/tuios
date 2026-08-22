// Package applist enumerates the programs a launcher can run.
//
// There are two sources. $PATH is the set a shell would run, and a launcher
// that disagrees with the shell is lying about what typing a name will do.
// .desktop files are the set the user's desktop already offers, and they carry
// what a $PATH listing cannot: the name a person knows the program by, an
// icon, a line of description, and an argv that may hold arguments the
// basename never reveals.
//
// Merge folds the second source into the first and returns the one list a
// launcher ranks. Entry stays source-agnostic so both halves go through the
// same matcher, and so a caller with a third source can join them the same
// way.
package applist

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// SourcePath names the $PATH source on an Entry and SourceDesktop the
// .desktop one, so a merged row can say where it came from.
//
// Why both are in one list is worth writing down, because the obvious way to
// do it is wrong. Almost every desktop entry's Exec is a program already on
// $PATH, so appending one source to the other puts the same program in the
// list twice under two names. The palette's whole job is that the right row is
// first, and near-synonyms work directly against that. Merge's supersession
// rule is what buys the human name and the icon without paying that price: a
// desktop entry that runs a $PATH program replaces that program's row instead
// of sitting next to it.
//
// A correct reading of a .desktop file is not small: Exec field codes, TryExec,
// NoDisplay, Hidden, OnlyShowIn, localised Name keys and Actions. Half of that
// produces rows that fail when activated, which is worse than a row that is not
// there. desktop.go implements the whole of it and drops what it cannot honour,
// so a merged row is one that runs.
//
// Desktop entries are Linux and BSD only, which is why that half of the package
// is unix-only and Merge does not exist elsewhere. $PATH means the same thing on
// every platform tuios builds for, so a Windows list is the $PATH list and stays
// as explicable as it was.
//
// One property of a desktop entry is tuios-specific: Terminal=true declares that
// the program must run inside a terminal emulator. tuios is one, so those are
// entries it hosts natively where a GUI launcher has to spawn a terminal first.
const (
	SourcePath    = "path"
	SourceDesktop = "desktop"
)

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

	// The fields below come from a .desktop file and are empty on a $PATH
	// entry, which has nothing but a name and a path to offer.

	// Display is the human name, such as "Firefox Web Browser". It is the name
	// the user knows the program by, which the basename often is not.
	Display string
	// Detail is GenericName, else Comment: the row's second piece of text. It
	// is what tells two entries with similar names apart.
	Detail string
	// Icon is the Icon= value, a themed name or an absolute path, left
	// unresolved because resolving it needs an IconFinder and the pixel size
	// the caller is drawing at.
	Icon string
	// Exec is Exec= tokenized to an argv, when the entry carries one. It is the
	// only way to run such an entry: Path is the .desktop file, not a program,
	// and the arguments are part of what the entry means.
	Exec []string
	// Cwd is the Path= value, the directory the entry asks to be started in.
	// Carried for a caller that can honour it; the tuios launcher does not yet,
	// so a desktop entry that depends on its working directory starts in
	// whatever one a new pane would have.
	Cwd string
	// Terminal reports Terminal=true: the entry has no window of its own and
	// needs a terminal emulator around it. tuios is one, which is why such an
	// entry is offered at all rather than filtered out. Nothing reads it yet;
	// it is the field a caller would check before deciding to wrap a GUI
	// launcher around one.
	Terminal bool
	// Keywords are the entry's Keywords=, words a person might search by that
	// appear in no other field. They feed Aliases.
	Keywords []string
}

// Label is what a launcher shows and matches against.
//
// Display wins when there is one because it is the name on the user's menus
// and the one they will type; Name is the fallback and is all a $PATH entry
// has.
func (e Entry) Label() string {
	if e.Display != "" {
		return e.Display
	}
	return e.Name
}

// Aliases are the extra strings a query may match, beyond Label.
//
// Someone after Firefox may type the binary name, or a word from the entry's
// Keywords such as "browser", and neither is the label. Matching those too
// keeps the row reachable by whichever name the person happens to know.
func (e Entry) Aliases() []string {
	var out []string
	if e.Display != "" && e.Name != "" && e.Name != e.Display {
		out = append(out, e.Name)
	}
	return append(out, e.Keywords...)
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
