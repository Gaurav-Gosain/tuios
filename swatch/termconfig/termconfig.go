// Package termconfig reads the colour scheme of a terminal's config file as
// a swatch.Palette: kitty, ghostty, alacritty and wezterm.
//
// All four formats say the same twenty things: a background, a text colour,
// a cursor and the sixteen ANSI colours. The difference is punctuation, so
// each format is reduced to one map of names, and one builder turns that
// into a palette. The format is told from the content, not the file name:
// kitty writes .conf, ghostty writes a file with no extension, and alacritty
// and wezterm both write .toml.
package termconfig

import (
	"errors"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"tuios.dev/swatch"
)

// Format names a terminal's colour scheme format.
type Format string

// The formats Parse reads. Wezterm and alacritty are both toml, told apart by
// where they keep the sixteen.
const (
	Kitty     Format = "kitty"
	Ghostty   Format = "ghostty"
	Alacritty Format = "alacritty"
	Wezterm   Format = "wezterm"
)

// SourceName is the Source field of every palette this package returns.
const SourceName = "termconfig"

// MaxFileSize is the most bytes of a config file Source reads.
const MaxFileSize = 1 << 20

// ErrNoScheme is Parse's error for content that is in none of the formats.
var ErrNoScheme = errors.New("not a kitty, ghostty, alacritty or wezterm colour scheme")

// The Raw keys of a palette Parse returns, for the colours the file named:
// foreground, background, cursor, and color0 to color15.
const (
	KeyForeground = "foreground"
	KeyBackground = "background"
	KeyCursor     = "cursor"
)

// ANSIKey is the Raw key of ANSI colour i, such as color4.
func ANSIKey(i int) string { return "color" + strconv.Itoa(i) }

// Parse reads a colour scheme in any of the four formats. Raw holds only the
// colours the file names, under the keys above. Every role of the palette is
// set: a colour the file does not name takes the xterm default, a bright one
// its normal one, and the cursor the text colour.
func Parse(data []byte) (swatch.Palette, Format, error) {
	colors, format, err := parse(string(data))
	if err != nil {
		return swatch.Palette{}, format, err
	}
	return build(colors), format, nil
}

func parse(body string) (map[string]color.Color, Format, error) {
	// Toml first: it is the only one that fails to parse as itself, so the
	// line formats are never mistaken for a broken toml file.
	if colors, format, ok := parseTOML(body); ok {
		return colors, format, nil
	}
	return parseLines(body)
}

// parseLines reads kitty's "key value" and ghostty's "key = value", which are
// the same format with and without an equals sign.
func parseLines(body string) (map[string]color.Color, Format, error) {
	colors := map[string]color.Color{}
	format := Kitty
	for line := range strings.Lines(body) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if ok {
			// An equals sign is ghostty's spelling. Kitty takes it too, so
			// this only settles which name to report.
			format = Ghostty
		} else {
			key, value, ok = strings.Cut(line, " ")
			if !ok {
				continue
			}
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		// ghostty's palette is one line per entry: palette = 4=#83a598.
		if key == "palette" {
			if idx, hex, ok := strings.Cut(value, "="); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(idx)); err == nil {
					setIndexed(colors, n, hex)
				}
			}
			continue
		}
		setNamed(colors, key, value)
	}
	if len(colors) == 0 {
		return nil, format, ErrNoScheme
	}
	return colors, format, nil
}

// alacrittyFile and weztermFile are the subset of each toml shape this reads.
type alacrittyFile struct {
	Colors struct {
		Primary struct {
			Foreground string `toml:"foreground"`
			Background string `toml:"background"`
		} `toml:"primary"`
		Cursor struct {
			Cursor string `toml:"cursor"`
		} `toml:"cursor"`
		Normal map[string]string `toml:"normal"`
		Bright map[string]string `toml:"bright"`
	} `toml:"colors"`
}

type weztermFile struct {
	Colors struct {
		Foreground string   `toml:"foreground"`
		Background string   `toml:"background"`
		Cursor     string   `toml:"cursor_bg"`
		ANSI       []string `toml:"ansi"`
		Brights    []string `toml:"brights"`
	} `toml:"colors"`
}

// parseTOML reads alacritty's and wezterm's toml: wezterm keeps the sixteen
// as two arrays, alacritty as two tables of named colours.
func parseTOML(body string) (map[string]color.Color, Format, bool) {
	colors := map[string]color.Color{}

	var wez weztermFile
	if err := toml.Unmarshal([]byte(body), &wez); err == nil {
		c := wez.Colors
		if len(c.ANSI) > 0 || len(c.Brights) > 0 {
			setNamed(colors, "foreground", c.Foreground)
			setNamed(colors, "background", c.Background)
			setNamed(colors, "cursor", c.Cursor)
			for i, hex := range c.ANSI {
				setIndexed(colors, i, hex)
			}
			for i, hex := range c.Brights {
				setIndexed(colors, i+8, hex)
			}
			if len(colors) > 0 {
				return colors, Wezterm, true
			}
		}
	}

	var ala alacrittyFile
	if err := toml.Unmarshal([]byte(body), &ala); err == nil {
		c := ala.Colors
		setNamed(colors, "foreground", c.Primary.Foreground)
		setNamed(colors, "background", c.Primary.Background)
		setNamed(colors, "cursor", c.Cursor.Cursor)
		for name, hex := range c.Normal {
			setNamed(colors, strings.ToLower(name), hex)
		}
		for name, hex := range c.Bright {
			setNamed(colors, "bright_"+strings.ToLower(name), hex)
		}
		if len(colors) > 0 {
			return colors, Alacritty, true
		}
	}
	return nil, "", false
}

// ansiNames are the sixteen by name, in index order, as alacritty spells
// them. purple is bubbletint's name for magenta.
var ansiNames = [16]string{
	"black", "red", "green", "yellow", "blue", "magenta", "cyan", "white",
	"bright_black", "bright_red", "bright_green", "bright_yellow",
	"bright_blue", "bright_magenta", "bright_cyan", "bright_white",
}

// setIndexed records ANSI colour n.
func setIndexed(colors map[string]color.Color, n int, value string) {
	if n < 0 || n >= 16 {
		return
	}
	if c, ok := parseColor(value); ok {
		colors[ANSIKey(n)] = c
	}
}

// setNamed records a colour under its Raw key. A name none of the formats
// uses, and a value that is not a colour, are skipped, so a partial scheme
// reads as far as it goes.
func setNamed(colors map[string]color.Color, name, value string) {
	key, ok := canonicalKey(name)
	if !ok {
		return
	}
	if c, ok := parseColor(value); ok {
		colors[key] = c
	}
}

// canonicalKey maps the spellings the four formats use onto the Raw keys.
func canonicalKey(name string) (string, bool) {
	switch name {
	case "foreground", "fg":
		return KeyForeground, true
	case "background", "bg":
		return KeyBackground, true
	case "cursor", "cursor-color", "cursor_bg", "cursor-colour":
		return KeyCursor, true
	case "purple":
		name = "magenta"
	case "bright_purple":
		name = "bright_magenta"
	}
	// kitty and ghostty write the sixteen as color0 to color15.
	for _, prefix := range []string{"color", "colour"} {
		if n, ok := strings.CutPrefix(name, prefix); ok {
			if i, err := strconv.Atoi(n); err == nil && i >= 0 && i < 16 {
				return ANSIKey(i), true
			}
		}
	}
	for i, n := range ansiNames {
		if name == n {
			return ANSIKey(i), true
		}
	}
	return "", false
}

// parseColor reads #rgb or #rrggbb, with or without the hash and in quotes
// or not, which is every way these formats write a colour.
func parseColor(v string) (color.Color, bool) {
	v = strings.TrimSpace(strings.Trim(strings.TrimSpace(v), `"'`))
	if !strings.HasPrefix(v, "#") {
		v = "#" + v
	}
	c, ok := swatch.ParseHex(v)
	if !ok {
		return nil, false
	}
	return c, true
}

// xterm is the colour of each ANSI slot a scheme leaves out.
var xterm = func() [8]color.Color {
	var out [8]color.Color
	for i, h := range []string{"#000000", "#cd0000", "#00cd00", "#cdcd00", "#0000ee", "#cd00cd", "#00cdcd", "#e5e5e5"} {
		out[i], _ = swatch.ParseHex(h)
	}
	return out
}()

// build fills a palette from the colours a file named.
func build(colors map[string]color.Color) swatch.Palette {
	p := swatch.Palette{Source: SourceName, Raw: colors}
	or := func(key string, fallback color.Color) color.Color {
		if c, ok := colors[key]; ok {
			return c
		}
		return fallback
	}
	p.Background = or(KeyBackground, xterm[0])
	p.Foreground = or(KeyForeground, xterm[7])
	p.Cursor = or(KeyCursor, p.Foreground)
	for i := range 8 {
		p.ANSI[i] = or(ANSIKey(i), xterm[i])
	}
	for i := 8; i < 16; i++ {
		p.ANSI[i] = or(ANSIKey(i), p.ANSI[i-8])
	}
	p.Light = luminance(p.Background) > 0.5
	p.SelectionBackground, p.SelectionForeground = p.ANSI[8], p.Foreground
	p.Surface, p.Muted = p.Background, p.ANSI[8]
	p.Error, p.Success, p.Warning, p.Info = p.ANSI[1], p.ANSI[2], p.ANSI[3], p.ANSI[4]
	p.Accent, p.Border = p.ANSI[12], p.ANSI[14]
	return p
}

func luminance(c color.Color) float64 {
	r, g, b, _ := c.RGBA()
	return (0.2126*float64(r) + 0.7152*float64(g) + 0.0722*float64(b)) / 0xffff
}

// Load reads and parses one file. The palette's ID is the file name without
// its extension, in lower case.
func Load(path string) (swatch.Palette, Format, error) {
	data, err := readCapped(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			err = fmt.Errorf("%w: %v", swatch.ErrNotFound, err)
		}
		return swatch.Palette{}, "", err
	}
	p, format, err := Parse(data)
	if err != nil {
		return p, format, fmt.Errorf("%s: %w", path, err)
	}
	base := filepath.Base(path)
	p.ID = strings.ToLower(strings.TrimSuffix(base, filepath.Ext(base)))
	p.Name = p.ID
	return p, format, nil
}

func readCapped(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if info.Size() > MaxFileSize {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, MaxFileSize)
	}
	return os.ReadFile(path) // #nosec G304 - the caller names the file
}

// Source returns a swatch.Source for one terminal config file. Its watch is
// on the file's directory, because editors and the terminals' own theme
// switchers replace the file rather than write it in place.
func Source(path string) swatch.Source { return fileSource{path: filepath.Clean(path)} }

type fileSource struct{ path string }

func (fileSource) Name() string { return SourceName }

func (s fileSource) Load() (swatch.Palette, error) {
	p, _, err := Load(s.path)
	return p, err
}

func (s fileSource) WatchPaths() []string { return []string{filepath.Dir(s.path)} }

func (s fileSource) Relevant(path string) bool { return filepath.Clean(path) == s.path }
