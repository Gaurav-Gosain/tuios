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

	mu      sync.Mutex
	bases   []string
	order   []string
	themes  map[string]*iconTheme
	dirSeen map[string]bool
	found   map[iconKey]string
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

var imageExts = map[string]struct{}{
	".png": {}, ".svg": {}, ".xpm": {}, ".jpg": {}, ".jpeg": {}, ".gif": {}, ".bmp": {},
}

func trimImageExt(name string) string {
	ext := filepath.Ext(name)
	if _, ok := imageExts[strings.ToLower(ext)]; !ok {
		return name
	}
	return name[:len(name)-len(ext)]
}

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
				break
			}
		}
	}
	return best
}

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

func (f *IconFinder) themeIndex(name string) *iconTheme {
	if t, ok := f.themes[name]; ok {
		return t
	}
	var t *iconTheme
	for _, base := range f.bases {
		if parsed := parseIconIndex(filepath.Join(base, name, "index.theme")); parsed != nil {
			t = parsed
			break
		}
	}
	f.themes[name] = t
	return t
}

func (f *IconFinder) hasDir(dir string) bool {
	if ok, seen := f.dirSeen[dir]; seen {
		return ok
	}
	info, err := os.Stat(dir)
	ok := err == nil && info.IsDir()
	f.dirSeen[dir] = ok
	return ok
}

type iconTheme struct {
	dirs     []iconDir
	inherits []string
}

type iconDir struct {
	path      string
	size      int
	minSize   int
	maxSize   int
	threshold int
	fixed     bool
	scalable  bool
}

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
			continue
		}
		switch {
		case group == "Icon Theme":
			switch key {
			case "Directories", "ScaledDirectories":
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

func iconFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
