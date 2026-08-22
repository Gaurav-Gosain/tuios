//go:build unix

package applist

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// HicolorTheme is the theme every other theme ends up inheriting from, and the
// only one the icon theme spec guarantees exists. It is the last themed place a
// lookup looks before giving up on themes entirely.
const HicolorTheme = "hicolor"

// iconExts and rasterIconExts are the extensions the spec defines, in the order
// it prefers them. Two lists rather than one filtered at each use, because the
// choice is made once per lookup and the order matters more than the saving.
var (
	iconExts       = []string{".png", ".svg", ".xpm"}
	rasterIconExts = []string{".png", ".xpm"}
)

// IconFinder resolves themed icon names to files on disk.
//
// One finder serves the whole process: it holds the parsed index.theme of every
// theme it has had to read and the answer to every name it has been asked,
// neither of which is cheap to work out and both of which are stable for as
// long as the icon directories are. A finder is safe for concurrent use.
type IconFinder struct {
	// RasterOnly makes Find ignore .svg files, so a name that exists only as
	// vector art resolves to nothing rather than to a file the caller cannot
	// open.
	//
	// It exists because the caller here is a terminal launcher that decodes
	// icons with Go's standard image packages, which read PNG, JPEG and GIF and
	// have no renderer for SVG. Skipping vectors outright, rather than letting
	// one win on size and fail at decode, is what lets a worse-sized PNG be
	// chosen while one exists, which is the outcome the launcher wants: an icon
	// scaled from the wrong size beats a blank cell. Set it before the first
	// Find. It is part of the cache key, so flipping it later is still correct,
	// it just pays for the walk again.
	RasterOnly bool

	theme string

	mu sync.Mutex
	// bases are the icon base directories in search order, resolved once from
	// the environment.
	bases []string
	// order is the theme names to search, in order, and is nil until the first
	// lookup forces the Inherits chain to be walked.
	order []string
	// themes holds one parsed index.theme per theme name. A nil value records a
	// theme with no readable index, so it is not opened a second time.
	themes map[string]*iconTheme
	// dirSeen answers whether a directory exists, because a lookup asks about
	// the same few hundred subdirectories once per icon name and most of them
	// are not there.
	dirSeen map[string]bool
	// found caches lookups, misses included. A missing icon is the common case
	// and the expensive one, since it is the only case that walks every
	// directory of every theme before it can answer.
	found map[iconKey]string
}

type iconKey struct {
	name   string
	size   int
	raster bool
}

// NewIconFinder builds a finder for the named theme, falling back through the
// theme's Inherits chain and then hicolor.
//
// It does no filesystem work: the base directories come from the environment,
// and each index.theme is parsed at the first lookup that needs it, so a caller
// that never asks for an icon never opens a theme.
func NewIconFinder(theme string) *IconFinder {
	if theme == "" {
		theme = HicolorTheme
	}
	return &IconFinder{
		theme:   theme,
		bases:   iconBaseDirs(),
		themes:  make(map[string]*iconTheme),
		dirSeen: make(map[string]bool),
		found:   make(map[iconKey]string),
	}
}

// Theme returns the theme the finder was built for, which is the first one it
// searches.
func (f *IconFinder) Theme() string { return f.theme }

// Find returns the best file for name at the given nominal pixel size, or ""
// when nothing matches. Safe for concurrent use.
//
// An absolute name is answered from that path alone and never starts a theme
// walk, because a .desktop file that names a file has already made the choice.
// Everything else is looked up in the requested theme, then the themes it
// inherits, then hicolor, then the unthemed directories, taking the closest
// size available in each before moving on to the next.
func (f *IconFinder) Find(name string, size int) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	if size <= 0 {
		size = 1
	}
	raster := f.RasterOnly
	if direct, isPath := f.direct(name, raster); isPath {
		return direct
	}
	// A relative name that carries an extension is not a path: resolving it
	// would mean resolving it against a working directory a launcher does not
	// have, and running off whatever sits in the current directory is how a
	// launcher becomes an attack. The extension is dropped and the bare name
	// searched, which is what such a value meant.
	name = trimImageExt(name)

	key := iconKey{name: name, size: size, raster: raster}
	f.mu.Lock()
	defer f.mu.Unlock()
	if path, ok := f.found[key]; ok {
		return path
	}
	path := f.lookup(name, size, raster)
	f.found[key] = path
	return path
}

// imageExts is what counts as an extension on an icon name. The list is
// deliberately short: most icon names on a modern system are reverse domain
// names, so anything trailing a dot is an extension by the usual reading and
// stripping it turns com.visualstudio.code.oss into com.visualstudio.code,
// which is nothing. Only a name that ends in a format someone could actually
// have meant as a file is treated as one.
var imageExts = map[string]struct{}{
	".png": {}, ".svg": {}, ".xpm": {}, ".jpg": {}, ".jpeg": {}, ".gif": {}, ".bmp": {},
}

// trimImageExt drops an image extension from an icon name, leaving every other
// kind of dotted name alone.
func trimImageExt(name string) string {
	ext := filepath.Ext(name)
	if _, ok := imageExts[strings.ToLower(ext)]; !ok {
		return name
	}
	return name[:len(name)-len(ext)]
}

// direct answers a name that already identifies a file, and reports whether it
// was such a name at all. The second result separates "this was a path and it
// is not there" from "this was an icon name", because only the first is
// finished and only the second is worth a theme walk.
func (f *IconFinder) direct(name string, raster bool) (string, bool) {
	if !filepath.IsAbs(name) {
		return "", false
	}
	if raster && strings.EqualFold(filepath.Ext(name), ".svg") {
		return "", true
	}
	if iconFile(name) {
		return name, true
	}
	return "", true
}

// lookup walks the themes in order and then the unthemed directories. The
// caller holds f.mu.
func (f *IconFinder) lookup(name string, size int, raster bool) string {
	exts := iconExts
	if raster {
		exts = rasterIconExts
	}
	for _, theme := range f.themeOrder() {
		if path := f.lookupIn(theme, name, size, exts); path != "" {
			return path
		}
	}
	// The unthemed fallback, for icons dropped straight into a base directory.
	// /usr/share/pixmaps is the one that earns its place: it is where packages
	// that predate icon themes still install, and where a real share of the
	// Icon= values on a real system are the only copy there is.
	for _, base := range f.bases {
		for _, ext := range exts {
			path := filepath.Join(base, name+ext)
			if iconFile(path) {
				return path
			}
		}
	}
	return ""
}

// lookupIn implements the spec's LookupIcon for one theme: the first
// size-matching directory that holds the icon wins outright, and failing that
// the directory with the smallest size distance does.
//
// The spec makes two passes over the directory list to say that. One is enough,
// because a directory that matches the size is returned the moment its file is
// found, and a directory that does not match can only ever improve the closest
// candidate, never beat a match found further down the list.
func (f *IconFinder) lookupIn(theme, name string, size int, exts []string) string {
	t := f.themeIndex(theme)
	if t == nil {
		return ""
	}
	best := ""
	bestDist := 0
	for i := range t.dirs {
		d := &t.dirs[i]
		matches := d.matches(size)
		dist := d.distance(size)
		if !matches && best != "" && dist >= bestDist {
			// Strictly worse than what is already held, so the stats could only
			// turn up a file that would then be discarded.
			continue
		}
		for _, base := range f.bases {
			dir := filepath.Join(base, theme, d.path)
			if !f.hasDir(dir) {
				continue
			}
			for _, ext := range exts {
				path := filepath.Join(dir, name+ext)
				if !iconFile(path) {
					continue
				}
				if matches {
					return path
				}
				if best == "" || dist < bestDist {
					best, bestDist = path, dist
				}
				// The extensions are in preference order, so the rest of them
				// in this directory can only be worse.
				break
			}
		}
	}
	return best
}

// themeOrder returns the theme names to search: the requested theme, then its
// Inherits chain breadth first, then hicolor.
//
// Breadth first is what the spec's recursion amounts to once a theme is
// forbidden from being visited twice, and forbidding that is what stops a theme
// which inherits itself, directly or around a ring of others, from looping
// forever. The caller holds f.mu.
func (f *IconFinder) themeOrder() []string {
	if f.order != nil {
		return f.order
	}
	seen := map[string]bool{}
	order := []string{}
	queue := []string{f.theme}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		order = append(order, name)
		if t := f.themeIndex(name); t != nil {
			queue = append(queue, t.inherits...)
		}
	}
	if !seen[HicolorTheme] {
		order = append(order, HicolorTheme)
	}
	f.order = order
	return order
}

// themeIndex returns the parsed index.theme for a theme name, parsing it at
// most once and returning nil for a theme with no readable index. The caller
// holds f.mu.
func (f *IconFinder) themeIndex(name string) *iconTheme {
	if t, ok := f.themes[name]; ok {
		return t
	}
	var t *iconTheme
	for _, base := range f.bases {
		if parsed := parseIconIndex(filepath.Join(base, name, "index.theme")); parsed != nil {
			// The first base directory holding an index defines the theme. A
			// later one may add files to it, but it does not get to redefine
			// the shape of a theme the user's own directory already described.
			t = parsed
			break
		}
	}
	f.themes[name] = t
	return t
}

// hasDir reports whether a directory exists, remembering the answer. Most of a
// theme's declared subdirectories are absent from most base directories, and
// without this every icon name pays a stat per extension for each of them.
func (f *IconFinder) hasDir(dir string) bool {
	if ok, seen := f.dirSeen[dir]; seen {
		return ok
	}
	info, err := os.Stat(dir)
	ok := err == nil && info.IsDir()
	f.dirSeen[dir] = ok
	return ok
}

// iconTheme is one theme's index.theme, reduced to the parts a lookup uses.
type iconTheme struct {
	dirs     []iconDir
	inherits []string
}

// iconDir is one subdirectory of a theme and the sizes it claims to hold.
type iconDir struct {
	path string
	// size, minSize, maxSize and threshold are in real pixels, with the
	// directory's Scale already multiplied in.
	//
	// The spec instead requires Scale to match exactly and treats size as
	// logical, which is right for a display server that knows its own scale
	// factor. A terminal draws one pixel per pixel and has no such factor, so a
	// directory marked Size=16 Scale=2 is simply where this theme's 32 pixel
	// art lives. Saying so is what lets those directories be used at all rather
	// than skipped, and hicolor declares one for every size it has.
	size      int
	minSize   int
	maxSize   int
	threshold int
	fixed     bool
	scalable  bool
}

// matches implements the spec's DirectoryMatchesSize.
func (d *iconDir) matches(size int) bool {
	switch {
	case d.fixed:
		return d.size == size
	case d.scalable:
		return d.minSize <= size && size <= d.maxSize
	default:
		return d.size-d.threshold <= size && size <= d.size+d.threshold
	}
}

// distance implements the spec's DirectorySizeDistance: how far the requested
// size falls outside what the directory holds, and zero when it does not fall
// outside at all.
func (d *iconDir) distance(size int) int {
	switch {
	case d.fixed:
		return iconAbs(d.size - size)
	case d.scalable:
		if size < d.minSize {
			return d.minSize - size
		}
		if size > d.maxSize {
			return size - d.maxSize
		}
		return 0
	default:
		if size < d.size-d.threshold {
			return d.minSize - size
		}
		if size > d.size+d.threshold {
			return size - d.maxSize
		}
		return 0
	}
}

func iconAbs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// parseIconIndex reads an index.theme and returns nil when it is missing or has
// no [Icon Theme] group.
//
// index.theme is the same key/value format as a .desktop file, but this reads
// it directly rather than through a desktop entry parser. The two want
// different things from a bad line: a desktop entry that will not parse must
// not be launched, while a theme index that will not parse fully is still worth
// the directories it did declare. A group this does not recognise, and a key it
// does not want, are skipped in silence.
func parseIconIndex(path string) *iconTheme {
	data, err := os.ReadFile(path) //nolint:gosec // the path is built from the icon base directories
	if err != nil {
		return nil
	}
	var (
		t       iconTheme
		group   string
		order   []string
		groups  = map[string]map[string]string{}
		listed  []string
		hasMain bool
	)
	for line := range strings.Lines(string(data)) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			group = line[1 : len(line)-1]
			if group == "Icon Theme" {
				hasMain = true
			} else if _, dup := groups[group]; !dup {
				groups[group] = map[string]string{}
				order = append(order, group)
			}
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if strings.Contains(key, "[") {
			// A translated key. Nothing read here is ever shown to anyone, so
			// the translation is noise.
			continue
		}
		switch {
		case group == "Icon Theme":
			switch key {
			case "Directories", "ScaledDirectories":
				// ScaledDirectories is where a theme puts its Scale>1
				// subdirectories when it wants them out of the way of a client
				// that only draws at 1x. This one measures a directory by the
				// pixels in it, so those are ordinary directories here.
				listed = append(listed, splitIconList(value)...)
			case "Inherits":
				t.inherits = append(t.inherits, splitIconList(value)...)
			}
		default:
			if g, ok := groups[group]; ok {
				g[key] = value
			}
		}
	}
	if !hasMain {
		return nil
	}
	// Directories names the subdirectories in the theme's own preferred order,
	// which is the order the spec searches in. A theme that omits the key, or
	// names a subdirectory it never goes on to describe, still gets whatever
	// groups it did describe: a theme that half declares itself is more useful
	// than no theme at all.
	if len(listed) == 0 {
		listed = order
	}
	t.dirs = make([]iconDir, 0, len(listed))
	for _, name := range listed {
		g, ok := groups[name]
		if !ok {
			continue
		}
		if d, ok := iconDirFrom(name, g); ok {
			t.dirs = append(t.dirs, d)
		}
	}
	return &t
}

// iconDirFrom turns one subdirectory group into an iconDir, applying the
// defaults the spec gives for every key but Size, which it requires and without
// which the directory cannot be measured against anything.
func iconDirFrom(path string, g map[string]string) (iconDir, bool) {
	size := iconInt(g["Size"], 0)
	if size <= 0 {
		return iconDir{}, false
	}
	scale := iconInt(g["Scale"], 1)
	if scale <= 0 {
		scale = 1
	}
	d := iconDir{
		path:      path,
		size:      size * scale,
		minSize:   iconInt(g["MinSize"], size) * scale,
		maxSize:   iconInt(g["MaxSize"], size) * scale,
		threshold: iconInt(g["Threshold"], 2) * scale,
	}
	switch strings.ToLower(g["Type"]) {
	case "fixed":
		d.fixed = true
	case "scalable":
		d.scalable = true
	}
	if d.minSize > d.maxSize {
		d.minSize, d.maxSize = d.maxSize, d.minSize
	}
	return d, true
}

func iconInt(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return fallback
	}
	return n
}

// splitIconList splits a comma separated key. Themes in the wild end the list
// with a trailing comma, so empty items are dropped rather than turned into a
// directory named "".
func splitIconList(value string) []string {
	raw := strings.Split(value, ",")
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// iconBaseDirs returns the icon base directories in the order the spec searches
// them: the user's own first, so a theme they installed wins over the system
// copy, then the system data directories, then pixmaps for the unthemed icons
// that predate all of this.
func iconBaseDirs() []string {
	var dirs []string
	seen := map[string]bool{}
	add := func(dir string) {
		if dir == "" || !filepath.IsAbs(dir) {
			return
		}
		dir = filepath.Clean(dir)
		if seen[dir] {
			return
		}
		seen[dir] = true
		dirs = append(dirs, dir)
	}

	home, _ := os.UserHomeDir()
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" && home != "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	add(filepath.Join(dataHome, "icons"))
	if home != "" {
		add(filepath.Join(home, ".icons"))
	}

	dataDirs := os.Getenv("XDG_DATA_DIRS")
	if dataDirs == "" {
		dataDirs = "/usr/local/share:/usr/share"
	}
	systems := strings.Split(dataDirs, string(os.PathListSeparator))
	for _, dir := range systems {
		add(filepath.Join(dir, "icons"))
	}
	// Pixmaps last, and derived from the same data directories, so a test or a
	// sandbox that moves XDG_DATA_DIRS moves this with it. The literal path is
	// added as well, because packages install there whatever the environment
	// happens to say.
	for _, dir := range systems {
		add(filepath.Join(dir, "pixmaps"))
	}
	add("/usr/share/pixmaps")
	return dirs
}

// CurrentIconTheme returns the user's configured icon theme, or "hicolor".
//
// There is no specification for this. The icon theme spec says how to find an
// icon once a theme is named and leaves the naming to the desktop, so the
// places the desktops that matter keep it are read in turn, newest GTK first.
// hicolor answers when none of them says anything, being the one theme
// guaranteed to exist.
func CurrentIconTheme() string {
	home, _ := os.UserHomeDir()
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" && home != "" {
		configHome = filepath.Join(home, ".config")
	}
	if configHome != "" {
		for _, rel := range []string{"gtk-4.0/settings.ini", "gtk-3.0/settings.ini"} {
			if v := iconIniValue(filepath.Join(configHome, rel), "Settings", "gtk-icon-theme-name"); v != "" {
				return v
			}
		}
		if v := iconIniValue(filepath.Join(configHome, "kdeglobals"), "Icons", "Theme"); v != "" {
			return v
		}
	}
	if home != "" {
		if v := iconIniValue(filepath.Join(home, ".gtkrc-2.0"), "", "gtk-icon-theme-name"); v != "" {
			return v
		}
	}
	return HicolorTheme
}

// iconIniValue reads one key from one group of a key/value file, where an empty
// group means the keys that appear before any group at all, as in .gtkrc-2.0.
// The value is unquoted, because GTK 2 quotes its strings and the others do
// not.
func iconIniValue(path, group, key string) string {
	data, err := os.ReadFile(path) //nolint:gosec // the path is built from the user's config directory
	if err != nil {
		return ""
	}
	current := ""
	for line := range strings.Lines(string(data)) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			current = line[1 : len(line)-1]
			continue
		}
		if current != group {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(k) != key {
			continue
		}
		return strings.Trim(strings.TrimSpace(v), `"`)
	}
	return ""
}

// iconFile reports whether path is a regular file, following symlinks, because a
// theme directory full of links into another theme is how distributions ship
// them and a dangling one must not be offered.
func iconFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
