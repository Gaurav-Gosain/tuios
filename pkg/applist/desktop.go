//go:build unix

package applist

import (
	"bufio"
	"cmp"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/sortx"
)

// DesktopEntry is one activatable entry read from a .desktop file: the
// application itself, or one of its Actions.
//
// An action carries its parent's identity and its parent's GenericName,
// Comment, Keywords, Path and Terminal, because it is the same application
// launched a different way. It does not inherit the parent's Icon: the spec
// gives an action group its own Icon key, so an action that does not set one
// has no icon of its own to show.
type DesktopEntry struct {
	// ID is the desktop-file ID, plus ":action" for an action.
	ID string
	// FileID is the owning file's ID, so an action can be traced to its
	// application.
	FileID string
	// Path is the absolute path to the .desktop file.
	Path string
	// Name is the localized Name. For an action it is "Parent - Verb", because
	// an action's own Name is only the verb and is meaningless on its own in a
	// flat list.
	Name string
	// Generic is the localized GenericName, such as "Web Browser". Often empty.
	Generic string
	// Comment is the localized Comment, the one-line description. Often empty.
	Comment string
	// Action is the action identifier, empty for the main entry.
	Action string
	// Argv is Exec= tokenized and field-code-resolved, ready for execve. It is
	// never empty and never contains a shell.
	Argv []string
	// WorkDir is Path=, the directory the program wants to be started in. Empty
	// means the launcher's own.
	WorkDir string
	// Icon is Icon=, either a themed icon name or an absolute path. May be
	// empty.
	Icon string
	// Terminal reports Terminal=true: the program has no window of its own and
	// must be run inside a terminal emulator. tuios is one, so these are the
	// entries it can host natively.
	Terminal bool
	// Keywords are the localized Keywords, extra words a person might search by
	// that do not appear in Name.
	Keywords []string
}

type entryGroup struct {
	keys map[string]string
}

type entryFile struct {
	groups map[string]*entryGroup
}

func (f *entryFile) group(name string) *entryGroup { return f.groups[name] }

func (g *entryGroup) raw(key string) (string, bool) {
	if g == nil {
		return "", false
	}
	v, ok := g.keys[key]
	if !ok {
		return "", false
	}
	return unescapeValue(v), true
}

func (g *entryGroup) str(key string) string { v, _ := g.raw(key); return v }

func (g *entryGroup) boolean(key string) bool {
	v, _ := g.raw(key)
	return v == "true"
}

func (g *entryGroup) list(key string) []string {
	if g == nil {
		return nil
	}
	v, ok := g.keys[key]
	if !ok {
		return nil
	}
	var out []string
	var cur strings.Builder
	for i := 0; i < len(v); i++ {
		if v[i] == '\\' && i+1 < len(v) && v[i+1] == ';' {
			cur.WriteByte(';')
			i++
			continue
		}
		if v[i] == ';' {
			out = append(out, unescapeValue(cur.String()))
			cur.Reset()
			continue
		}
		cur.WriteByte(v[i])
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		out = append(out, unescapeValue(s))
	}
	return out
}

func (g *entryGroup) localizedList(key string, loc locale) []string {
	if g == nil {
		return nil
	}
	for _, suffix := range loc.candidates() {
		if _, ok := g.keys[key+"["+suffix+"]"]; ok {
			return g.list(key + "[" + suffix + "]")
		}
	}
	return g.list(key)
}

func (g *entryGroup) localized(key string, loc locale) string {
	if g == nil {
		return ""
	}
	for _, suffix := range loc.candidates() {
		if v, ok := g.raw(key + "[" + suffix + "]"); ok {
			return v
		}
	}
	return g.str(key)
}

func unescapeValue(v string) string {
	if !strings.ContainsRune(v, '\\') {
		return v
	}
	var b strings.Builder
	b.Grow(len(v))
	for i := 0; i < len(v); i++ {
		if v[i] != '\\' || i+1 >= len(v) {
			b.WriteByte(v[i])
			continue
		}
		i++
		switch v[i] {
		case 's':
			b.WriteByte(' ')
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case '\\':
			b.WriteByte('\\')
		default:
			b.WriteByte('\\')
			b.WriteByte(v[i])
		}
	}
	return b.String()
}

func parseEntryFile(path string) (*entryFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	ef := &entryFile{groups: map[string]*entryGroup{}}
	var cur *entryGroup
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		line = strings.TrimPrefix(line, "\ufeff")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			name := trimmed[1 : len(trimmed)-1]
			if g, ok := ef.groups[name]; ok {
				cur = g
			} else {
				cur = &entryGroup{keys: map[string]string{}}
				ef.groups[name] = cur
			}
			continue
		}
		if cur == nil {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimLeft(line[eq+1:], " \t")
		if key == "" {
			continue
		}
		if _, dup := cur.keys[key]; dup {
			continue
		}
		cur.keys[key] = val
	}
	return ef, sc.Err()
}

type locale struct{ lang, country, modifier string }

func currentLocale() locale {
	v := ""
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if s := os.Getenv(k); s != "" {
			v = s
			break
		}
	}
	if v == "" || v == "C" || v == "POSIX" {
		return locale{}
	}
	var l locale
	if i := strings.IndexByte(v, '@'); i >= 0 {
		l.modifier = v[i+1:]
		v = v[:i]
	}
	if i := strings.IndexByte(v, '.'); i >= 0 {
		v = v[:i]
	}
	if i := strings.IndexByte(v, '_'); i >= 0 {
		l.country = v[i+1:]
		v = v[:i]
	}
	l.lang = v
	return l
}

func (l locale) candidates() []string {
	if l.lang == "" {
		return nil
	}
	var out []string
	if l.country != "" && l.modifier != "" {
		out = append(out, l.lang+"_"+l.country+"@"+l.modifier)
	}
	if l.country != "" {
		out = append(out, l.lang+"_"+l.country)
	}
	if l.modifier != "" {
		out = append(out, l.lang+"@"+l.modifier)
	}
	return append(out, l.lang)
}

// DesktopDirs returns the XDG application directories, highest precedence
// first: $XDG_DATA_HOME/applications, then applications/ under each entry of
// $XDG_DATA_DIRS in order.
//
// The order is the whole override mechanism, so it is never sorted or
// deduplicated on the way out.
func DesktopDirs() []string {
	home := os.Getenv("XDG_DATA_HOME")
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = filepath.Join(h, ".local", "share")
		}
	}
	dirs := os.Getenv("XDG_DATA_DIRS")
	if dirs == "" {
		dirs = "/usr/local/share:/usr/share"
	}
	var out []string
	if home != "" {
		out = append(out, filepath.Join(home, "applications"))
	}
	for d := range strings.SplitSeq(dirs, ":") {
		if d == "" {
			continue
		}
		out = append(out, filepath.Join(d, "applications"))
	}
	return out
}

func currentDesktops() []string {
	v := os.Getenv("XDG_CURRENT_DESKTOP")
	if v == "" {
		return nil
	}
	var out []string
	for s := range strings.SplitSeq(v, ":") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// ScanDesktop reads every .desktop file under dirs and returns the entries
// worth offering, sorted by Name then ID.
//
// Entries are dropped for the reasons the spec gives: a Type other than
// Application, Hidden=true, an OnlyShowIn/NotShowIn that excludes the current
// $XDG_CURRENT_DESKTOP, a TryExec that does not resolve, NoDisplay=true, and a
// blank or unparsable Exec. Locale and desktop names come from the environment;
// dirs is a parameter so a caller can scan somewhere else.
//
// ScanDesktop parses every file it finds. Call it off any goroutine that must
// stay responsive, or use DesktopCache.
func ScanDesktop(dirs []string) []DesktopEntry {
	loc := currentLocale()
	desktops := currentDesktops()
	var out []DesktopEntry
	walkDesktopFiles(dirs, func(path, id string) {
		out = append(out, entriesFromFile(path, id, loc, desktops)...)
	})
	sortDesktopEntries(out)
	return out
}

func walkDesktopFiles(dirs []string, fn func(path, id string)) {
	seen := make(map[string]struct{}, 512)
	for _, dir := range dirs {
		root, err := filepath.Abs(dir)
		if err != nil {
			continue
		}
		_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(p, ".desktop") {
				return nil
			}
			rel, rerr := filepath.Rel(root, p)
			if rerr != nil {
				return nil
			}
			id := strings.ReplaceAll(filepath.ToSlash(rel), "/", "-")
			if _, dup := seen[id]; dup {
				return nil
			}
			seen[id] = struct{}{}
			fn(p, id)
			return nil
		})
	}
}

func sortDesktopEntries(entries []DesktopEntry) {
	sortx.SortFunc(entries, func(a, b DesktopEntry) int {
		if c := cmp.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
}

func entriesFromFile(path, id string, loc locale, desktops []string) []DesktopEntry {
	ef, err := parseEntryFile(path)
	if err != nil {
		return nil
	}
	g := ef.group("Desktop Entry")
	if g == nil {
		return nil
	}
	if g.str("Type") != "Application" {
		return nil
	}
	if g.boolean("Hidden") {
		return nil
	}
	if !showIn(g, desktops) {
		return nil
	}
	if try := g.str("TryExec"); try != "" && !resolvesInPath(try) {
		return nil
	}
	if g.boolean("NoDisplay") {
		return nil
	}

	name := g.localized("Name", loc)
	if name == "" {
		name = strings.TrimSuffix(filepath.Base(path), ".desktop")
	}

	base := DesktopEntry{
		ID:       id,
		FileID:   id,
		Path:     path,
		Name:     name,
		Generic:  g.localized("GenericName", loc),
		Comment:  g.localized("Comment", loc),
		WorkDir:  g.str("Path"),
		Icon:     g.str("Icon"),
		Terminal: g.boolean("Terminal"),
		Keywords: g.localizedList("Keywords", loc),
	}

	argv, ok := entryArgv(g.str("Exec"), path, name, base.Icon)
	if !ok {
		return nil
	}
	base.Argv = argv
	out := []DesktopEntry{base}

	for _, an := range g.list("Actions") {
		ag := ef.group("Desktop Action " + an)
		if ag == nil {
			continue
		}
		if ag.boolean("Hidden") || ag.boolean("NoDisplay") {
			continue
		}
		verb := ag.localized("Name", loc)
		if verb == "" {
			verb = an
		}
		aicon := ag.str("Icon")
		aargv, aok := entryArgv(ag.str("Exec"), path, name, aicon)
		if !aok {
			continue
		}
		a := base
		a.ID = id + ":" + an
		a.Action = an
		a.Name = name + " - " + verb
		a.Icon = aicon
		a.Argv = aargv
		out = append(out, a)
	}
	return out
}

func entryArgv(execVal, path, nameVal, iconVal string) ([]string, bool) {
	if strings.TrimSpace(execVal) == "" {
		return nil, false
	}
	argv, err := parseExec(execVal, path, nameVal, iconVal)
	if err != nil || len(argv) == 0 {
		return nil, false
	}
	return argv, true
}

func showIn(g *entryGroup, desktops []string) bool {
	if only := g.list("OnlyShowIn"); len(only) > 0 {
		for _, d := range desktops {
			if slices.Contains(only, d) {
				return true
			}
		}
		return false
	}
	for _, n := range g.list("NotShowIn") {
		if slices.Contains(desktops, n) {
			return false
		}
	}
	return true
}

func resolvesInPath(name string) bool {
	if strings.ContainsRune(name, '/') {
		st, err := os.Stat(name)
		return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
	}
	_, err := exec.LookPath(name)
	return err == nil
}

// DesktopCache holds a scan and reparses only the files that changed.
//
// The unit is a single file rather than a directory, unlike the $PATH Cache,
// because the costs are not comparable: a $PATH entry is a readdir result that
// is cheap to re-read, while a .desktop file has to be opened, scanned into
// groups, localized and tokenized. A typical system has a couple of hundred of
// them, so reopening a launcher restats each and reparses none.
//
// The environment feeds the scan too, through the locale and
// $XDG_CURRENT_DESKTOP, so a change to either discards the whole cache: the
// stored entries were localized and filtered under the old values.
//
// A DesktopCache is safe for concurrent use.
type DesktopCache struct {
	mu      sync.Mutex
	env     string
	files   map[string]cachedDesktopFile
	entries []DesktopEntry
}

type cachedDesktopFile struct {
	mtime   time.Time
	entries []DesktopEntry
}

// NewDesktopCache returns an empty cache. Its first Refresh is a full scan.
func NewDesktopCache() *DesktopCache {
	return &DesktopCache{files: make(map[string]cachedDesktopFile)}
}

// Entries returns the most recent scan without touching the filesystem, so it
// is safe to call from a render or input path. It is nil before the first
// Refresh.
func (c *DesktopCache) Entries() []DesktopEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.entries
}

// Refresh reparses the .desktop files whose mtime moved and reports the current
// list along with whether anything changed. It does filesystem I/O; call it off
// the goroutine that has to stay responsive.
func (c *DesktopCache) Refresh() ([]DesktopEntry, bool) {
	return c.refresh(DesktopDirs())
}

func (c *DesktopCache) refresh(dirs []string) ([]DesktopEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	loc := currentLocale()
	desktops := currentDesktops()
	env := strings.Join(loc.candidates(), ",") + "\x00" + strings.Join(desktops, ",")
	if env != c.env {
		c.env = env
		c.files = make(map[string]cachedDesktopFile, len(c.files))
	}

	fresh := make(map[string]cachedDesktopFile, len(c.files))
	var out []DesktopEntry
	changed := false

	walkDesktopFiles(dirs, func(path, id string) {
		var mtime time.Time
		if info, err := os.Stat(path); err == nil {
			mtime = info.ModTime()
		}
		prev, had := c.files[path]
		if had && !mtime.IsZero() && prev.mtime.Equal(mtime) {
			fresh[path] = prev
			out = append(out, prev.entries...)
			return
		}
		changed = true
		parsed := entriesFromFile(path, id, loc, desktops)
		fresh[path] = cachedDesktopFile{mtime: mtime, entries: parsed}
		out = append(out, parsed...)
	})

	if len(fresh) != len(c.files) {
		changed = true
	}
	c.files = fresh
	if !changed {
		return c.entries, false
	}
	sortDesktopEntries(out)
	c.entries = out
	return out, true
}
