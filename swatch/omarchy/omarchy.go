// Package omarchy reads the theme Omarchy applies, as a swatch.Palette.
//
// Omarchy 4 keeps the applied theme in ~/.local/state/omarchy/current/theme,
// and Omarchy 3 kept it in ~/.config/omarchy/current/theme. Either way the
// palette is the theme's colors.toml. A theme switch replaces the theme
// directory whole and then writes theme.name beside it, so a watch on the
// current directory sees every switch.
//
// Current is the Source for the applied theme. Themes lists every installed
// theme, and Theme.Load reads one of them without applying it.
package omarchy

import (
	"errors"
	"fmt"
	"image/color"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"tuios.dev/swatch"
)

// SourceName is the Source field of every palette this package returns.
const SourceName = "omarchy"

// EnvDir names an environment variable that, when set, is the one current
// directory Current reads, in place of the two Omarchy uses. It is for tests
// and unusual installs.
const EnvDir = "SWATCH_OMARCHY_DIR"

// CurrentDirs returns the directories Omarchy keeps the applied theme in,
// newest layout first: the value of EnvDir alone when it is set, or the
// Omarchy 4 and the Omarchy 3 directory under home. Omarchy uses
// $HOME/.local/state literally, whatever XDG_STATE_HOME says.
func CurrentDirs(home string) []string {
	if dir := os.Getenv(EnvDir); dir != "" {
		return []string{dir}
	}
	return []string{
		filepath.Join(home, ".local", "state", "omarchy", "current"),
		filepath.Join(home, ".config", "omarchy", "current"),
	}
}

// Current returns the Source for the theme Omarchy has applied on this
// machine, found from the user's home directory.
func Current() swatch.Source {
	home, _ := os.UserHomeDir()
	return At(CurrentDirs(home)...)
}

// At returns the Source for the theme applied in the first of dirs that holds
// one. Each dir is a current directory: the one that holds theme/ and
// theme.name.
func At(dirs ...string) swatch.Source { return currentSource{dirs: dirs} }

type currentSource struct{ dirs []string }

func (currentSource) Name() string { return SourceName }

func (s currentSource) WatchPaths() []string { return s.dirs }

// Relevant is true for the theme directory, theme.name and theme.generation
// of any current directory, and for a current directory itself, which
// appears when Omarchy is installed.
func (s currentSource) Relevant(path string) bool {
	path = filepath.Clean(path)
	for _, dir := range s.dirs {
		dir = filepath.Clean(dir)
		if path == dir {
			return true
		}
		if filepath.Dir(path) != dir {
			continue
		}
		switch filepath.Base(path) {
		case "theme", "theme.name", "theme.generation":
			return true
		}
	}
	return false
}

// Load reads the first current directory that holds a theme or a theme.name.
// It does not fall through to an older layout once a newer one is in use: a
// machine upgraded from Omarchy 3 can keep a stale ~/.config/omarchy/current,
// and a read in the gap of an Omarchy 4 switch must fail, so the watcher keeps
// the palette in force, rather than serve that stale theme.
func (s currentSource) Load() (swatch.Palette, error) {
	for _, dir := range s.dirs {
		if !exists(filepath.Join(dir, "theme")) && !exists(filepath.Join(dir, "theme.name")) {
			continue
		}
		return loadCurrent(dir)
	}
	return swatch.Palette{}, fmt.Errorf("omarchy: no applied theme: %w", swatch.ErrNotFound)
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// loadCurrent reads the theme applied in one current directory.
func loadCurrent(dir string) (swatch.Palette, error) {
	theme := filepath.Join(dir, "theme")
	id := ""
	if data, err := readCapped(filepath.Join(dir, "theme.name"), 256); err == nil {
		id = strings.TrimSpace(string(data))
	}
	return load(theme, id)
}

// load reads the colors.toml in dir as the theme id.
func load(dir, id string) (swatch.Palette, error) {
	data, err := readCapped(filepath.Join(dir, "colors.toml"), MaxFileSize)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			err = fmt.Errorf("omarchy: %w: %v", swatch.ErrNotFound, err)
		}
		return swatch.Palette{}, err
	}
	_, statErr := os.Stat(filepath.Join(dir, "light.mode"))
	colors := Resolve(data, statErr == nil)
	if colors["background"] == "" {
		// An empty or broken file, or one caught half written. Resolve
		// derives nothing without a background, and a black on white
		// palette from it would replace the theme in force.
		return swatch.Palette{}, fmt.Errorf("omarchy: %s names no background", filepath.Join(dir, "colors.toml"))
	}
	p := FromColors(colors)
	p.ID, p.Name = id, DisplayName(id)
	return p, nil
}

// readCapped reads a regular file of at most limit bytes.
func readCapped(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("omarchy: %s is not a regular file", path)
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("omarchy: %s is larger than %d bytes", path, limit)
	}
	return data, nil
}

// DisplayName turns a theme directory name into the name Omarchy shows:
// "tokyo-night" is "Tokyo Night".
func DisplayName(id string) string {
	b := []byte(id)
	for i, c := range b {
		if c >= 'a' && c <= 'z' && (i == 0 || id[i-1] == '-') {
			b[i] = c - 'a' + 'A'
		}
	}
	return strings.ReplaceAll(string(b), "-", " ")
}

// FromColors builds a palette from the keys Resolve returns. The ANSI
// colours, the cursor and the selection follow Omarchy's own terminal
// templates, so a palette here matches a terminal Omarchy themed. A key whose
// value is not a #rrggbb or #rgb colour is treated as absent, and a role it
// leaves empty falls back to the nearest role that is set.
func FromColors(c map[string]string) swatch.Palette {
	raw := map[string]color.Color{}
	for k, v := range c {
		if col, ok := swatch.ParseHex(v); ok {
			raw[k] = col
		}
	}
	pick := func(keys ...string) color.Color {
		for _, k := range keys {
			if col, ok := raw[k]; ok {
				return col
			}
		}
		return nil
	}
	p := swatch.Palette{
		Source: SourceName,
		Light:  c["mode"] == "light",
		Raw:    raw,
	}
	def := map[bool][2]color.Color{
		false: {color.RGBA{A: 0xff}, color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}},
		true:  {color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}, color.RGBA{A: 0xff}},
	}[p.Light]
	p.Background = pick("background")
	if p.Background == nil {
		p.Background = def[0]
	}
	p.Foreground = pick("foreground")
	if p.Foreground == nil {
		p.Foreground = def[1]
	}
	for i, k := range ansiKeys {
		p.ANSI[i] = pick(k)
	}
	p.ANSI[0], p.ANSI[7] = p.Background, p.Foreground
	for i := range p.ANSI {
		if p.ANSI[i] == nil {
			// A bright slot falls back to its normal one, and a normal
			// one to the text colour.
			if i >= 8 && p.ANSI[i-8] != nil {
				p.ANSI[i] = p.ANSI[i-8]
			} else {
				p.ANSI[i] = p.Foreground
			}
		}
	}
	or := func(c color.Color, fallback color.Color) color.Color {
		if c != nil {
			return c
		}
		return fallback
	}
	p.Cursor = or(pick("cursor"), p.Foreground)
	p.SelectionBackground = or(pick("selection_background"), p.ANSI[8])
	p.SelectionForeground = or(pick("selection_foreground"), p.Foreground)
	p.Surface = or(pick("lighter_background"), p.Background)
	p.Muted = or(pick("muted"), p.ANSI[8])
	p.Error, p.Success, p.Warning, p.Info = p.ANSI[1], p.ANSI[2], p.ANSI[3], p.ANSI[4]
	p.Accent = or(pick("accent"), p.Info)
	p.Border = or(gradientStart(c["hyprland_active_border"], c), p.Accent)
	return p
}

// gradientStart is the first colour of a Hyprland border spec, such as
// "rgba(26a269ee) rgba(2ec27eee) 45deg", as gradient_start in Omarchy's
// template renderer reads it. A stop may also name another key. It returns
// nil when the spec has no colour it can read. Alpha is dropped.
func gradientStart(spec string, c map[string]string) color.Color {
	for part := range strings.FieldsSeq(spec) {
		if isAngle(part) {
			continue
		}
		if v, ok := c[part]; ok {
			part = v
		}
		col, ok := hyprColor(part)
		if !ok {
			return nil
		}
		return col
	}
	return nil
}

// isAngle matches -?[0-9]+(\.[0-9]+)?deg.
func isAngle(s string) bool {
	s, ok := strings.CutSuffix(s, "deg")
	if !ok {
		return false
	}
	s = strings.TrimPrefix(s, "-")
	whole, frac, dot := strings.Cut(s, ".")
	return digits(whole) && (!dot || digits(frac))
}

func digits(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// hyprColor reads #rrggbb, #rrggbbaa, rgb(rrggbb), rgba(rrggbbaa) and
// rgb(r,g,b) or rgba(r,g,b,a), the forms Omarchy's renderer accepts.
func hyprColor(s string) (color.Color, bool) {
	if strings.HasPrefix(s, "#") && (len(s) == 7 || len(s) == 9) {
		col, ok := swatch.ParseHex(s[:7])
		return col, ok
	}
	lowerS := strings.ToLower(s)
	var body string
	switch {
	case strings.HasPrefix(lowerS, "rgba(") && strings.HasSuffix(s, ")"):
		body = s[5 : len(s)-1]
	case strings.HasPrefix(lowerS, "rgb(") && strings.HasSuffix(s, ")"):
		body = s[4 : len(s)-1]
	default:
		return nil, false
	}
	if (len(body) == 6 || len(body) == 8) && !strings.Contains(body, ",") {
		col, ok := swatch.ParseHex("#" + body[:6])
		return col, ok
	}
	parts := strings.Split(body, ",")
	if len(parts) != 3 && len(parts) != 4 {
		return nil, false
	}
	var ch [3]uint8
	for i := range 3 {
		if !digits(parts[i]) {
			return nil, false
		}
		v := 0
		for _, d := range parts[i] {
			v = min(v*10+int(d-'0'), 256)
		}
		ch[i] = uint8(min(v, 255))
	}
	return color.RGBA{R: ch[0], G: ch[1], B: ch[2], A: 0xff}, true
}
