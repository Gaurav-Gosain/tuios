package theme

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	tint "github.com/lrstanley/bubbletint/v2"
	"github.com/pelletier/go-toml/v2"
)

// Converting a terminal's colour scheme into a tuios theme is the most common
// thing anyone means by "rice this to match my setup": the palette already
// exists on disk, in kitty's conf or ghostty's config or an alacritty or
// wezterm toml, and what was missing was the twenty lines of parsing between
// there and a theme file. Doing it by hand meant reading four formats and
// knowing which of the sixteen slots each key fills, which is exactly the sort
// of transcription that goes wrong quietly: one colour in the wrong slot looks
// like a theme that half-applied.
//
// All four formats say the same twenty things. The difference is punctuation,
// so the parsers below reduce each to one map of canonical names and a single
// builder turns that into a theme.

// ImportFormat names a terminal's colour scheme format.
type ImportFormat string

// The formats Import understands. Wezterm and alacritty are both toml and are
// told apart by the table they put the colours in.
const (
	FormatKitty     ImportFormat = "kitty"
	FormatGhostty   ImportFormat = "ghostty"
	FormatAlacritty ImportFormat = "alacritty"
	FormatWezterm   ImportFormat = "wezterm"
)

// hexPattern is the literal every one of these formats writes a colour as,
// with or without the leading hash.
var hexPattern = regexp.MustCompile(`^#?([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// Import reads a terminal colour scheme and returns it as a tuios theme.
//
// The format is sniffed from the content rather than the extension, because
// none of these formats has one it can be relied on for: kitty writes .conf,
// ghostty writes a file with no extension at all, and alacritty and wezterm
// both write .toml.
func Import(path, id string) (*tint.Tint, ImportFormat, error) {
	// #nosec G304 - the caller names a file it already has; reading it is the point
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("read %s: %w", path, err)
	}

	colors, format, err := parseScheme(string(data))
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", path, err)
	}
	if len(colors) == 0 {
		return nil, format, fmt.Errorf("%s: read as %s and found no colours in it", path, format)
	}

	if id == "" {
		base := filepath.Base(path)
		id = strings.ToLower(strings.TrimSuffix(base, filepath.Ext(base)))
	}

	t := &tint.Tint{ID: id, DisplayName: id}
	assign(t, colors)
	// A scheme that names no background is taken as dark, which is what all
	// four formats default to and what every scheme that omits it means.
	t.Dark = isDark(t.Bg)
	fillDefaults(t)
	return t, format, nil
}

// WriteTheme writes a theme into the custom themes directory and registers it,
// returning the file it wrote.
func WriteTheme(t *tint.Tint) (string, error) {
	dir, err := GetThemesDir()
	if err != nil {
		return "", err
	}
	body, err := json.MarshalIndent(t, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode theme: %w", err)
	}
	path := filepath.Join(dir, t.ID+".json")
	if err := os.WriteFile(path, append(body, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	// Register it here as well as on disk so the process that imported it can
	// select the theme without re-reading the directory.
	EnsureRegistry()
	tint.Register(t)
	return path, nil
}

// parseScheme reduces any of the four formats to canonical colour names.
func parseScheme(body string) (map[string]string, ImportFormat, error) {
	// Toml first: it is the only one that fails to parse as itself, so trying it
	// first means the line formats are never mistaken for a broken toml file.
	if colors, format, ok := parseTOMLScheme(body); ok {
		return colors, format, nil
	}
	return parseLineScheme(body)
}

// parseLineScheme reads kitty's "key value" and ghostty's "key = value", which
// are the same format with and without an equals sign.
func parseLineScheme(body string) (map[string]string, ImportFormat, error) {
	colors := map[string]string{}
	format := FormatKitty

	for line := range strings.Lines(body) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "//") {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if ok {
			// An equals sign is ghostty's spelling. Kitty accepts it too, so this
			// only settles which name to report.
			format = FormatGhostty
		} else {
			key, value, ok = strings.Cut(line, " ")
			if !ok {
				continue
			}
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)

		// ghostty's palette is a whole line per entry: palette = 4=#83a598.
		if key == "palette" {
			if idx, hex, ok := strings.Cut(value, "="); ok {
				if n, err := strconv.Atoi(strings.TrimSpace(idx)); err == nil {
					setIndexed(colors, n, strings.TrimSpace(hex))
				}
			}
			continue
		}
		if name, ok := canonicalKey(key); ok && isHex(value) {
			colors[name] = normalizeHex(value)
		}
	}

	if len(colors) == 0 {
		return nil, format, fmt.Errorf("not a kitty, ghostty, alacritty or wezterm colour scheme")
	}
	return colors, format, nil
}

// alacrittyScheme and weztermScheme are the two toml shapes, declared as the
// subset of each file this reads. Everything else in a real config (fonts,
// keybinds, a whole wezterm module) is left to the decoder to ignore.
type alacrittyScheme struct {
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

type weztermScheme struct {
	Colors struct {
		Foreground string   `toml:"foreground"`
		Background string   `toml:"background"`
		Cursor     string   `toml:"cursor_bg"`
		ANSI       []string `toml:"ansi"`
		Brights    []string `toml:"brights"`
	} `toml:"colors"`
}

// parseTOMLScheme reads alacritty's and wezterm's toml. The two are told apart
// by shape: wezterm keeps the sixteen as two arrays, alacritty as two tables of
// named colours.
func parseTOMLScheme(body string) (map[string]string, ImportFormat, bool) {
	colors := map[string]string{}

	var wez weztermScheme
	if err := toml.Unmarshal([]byte(body), &wez); err == nil {
		c := wez.Colors
		if len(c.ANSI) > 0 || len(c.Brights) > 0 {
			setNamed(colors, "fg", c.Foreground)
			setNamed(colors, "bg", c.Background)
			setNamed(colors, "cursor", c.Cursor)
			for i, hex := range c.ANSI {
				setIndexed(colors, i, hex)
			}
			for i, hex := range c.Brights {
				setIndexed(colors, i+8, hex)
			}
			if len(colors) > 0 {
				return colors, FormatWezterm, true
			}
		}
	}

	var ala alacrittyScheme
	if err := toml.Unmarshal([]byte(body), &ala); err == nil {
		c := ala.Colors
		setNamed(colors, "fg", c.Primary.Foreground)
		setNamed(colors, "bg", c.Primary.Background)
		setNamed(colors, "cursor", c.Cursor.Cursor)
		for name, hex := range c.Normal {
			setNamed(colors, strings.ToLower(name), hex)
		}
		for name, hex := range c.Bright {
			setNamed(colors, "bright_"+strings.ToLower(name), hex)
		}
		if len(colors) > 0 {
			return colors, FormatAlacritty, true
		}
	}

	return nil, "", false
}

// ansiIndexNames maps a palette index to the canonical name for that slot.
var ansiIndexNames = ansiNames

// setIndexed records colour n of the sixteen.
func setIndexed(colors map[string]string, n int, hex string) {
	if n < 0 || n >= len(ansiIndexNames) || !isHex(hex) {
		return
	}
	colors[ansiIndexNames[n]] = normalizeHex(hex)
}

// setNamed records a colour under a canonical name, ignoring the empty and the
// unparseable so a partial scheme imports as far as it goes.
func setNamed(colors map[string]string, name, hex string) {
	if !isHex(hex) {
		return
	}
	if canonical, ok := canonicalKey(name); ok {
		colors[canonical] = normalizeHex(hex)
	}
}

// canonicalKey maps the spellings the four formats use onto one set of names.
func canonicalKey(key string) (string, bool) {
	switch key {
	case "foreground", "fg":
		return "fg", true
	case "background", "bg":
		return "bg", true
	case "cursor", "cursor-color", "cursor_bg", "cursor-colour":
		return "cursor", true
	}
	// kitty and ghostty write the sixteen as color0..color15.
	for _, prefix := range []string{"color", "colour"} {
		if n, ok := strings.CutPrefix(key, prefix); ok {
			if i, err := strconv.Atoi(n); err == nil && i >= 0 && i < len(ansiIndexNames) {
				return ansiIndexNames[i], true
			}
		}
	}
	// alacritty writes them by name, and "magenta" is what everyone but
	// bubbletint calls purple.
	switch key {
	case "magenta":
		return "purple", true
	case "bright_magenta":
		return "bright_purple", true
	}
	for _, name := range ansiIndexNames {
		if key == name {
			return name, true
		}
	}
	return "", false
}

// assign writes the canonical map onto a theme.
func assign(t *tint.Tint, colors map[string]string) {
	slots := map[string]**tint.Color{
		"fg":     &t.Fg,
		"bg":     &t.Bg,
		"cursor": &t.Cursor,

		"black":  &t.Black,
		"red":    &t.Red,
		"green":  &t.Green,
		"yellow": &t.Yellow,
		"blue":   &t.Blue,
		"purple": &t.Purple,
		"cyan":   &t.Cyan,
		"white":  &t.White,

		"bright_black":  &t.BrightBlack,
		"bright_red":    &t.BrightRed,
		"bright_green":  &t.BrightGreen,
		"bright_yellow": &t.BrightYellow,
		"bright_blue":   &t.BrightBlue,
		"bright_purple": &t.BrightPurple,
		"bright_cyan":   &t.BrightCyan,
		"bright_white":  &t.BrightWhite,
	}
	for name, hex := range colors {
		if slot, ok := slots[name]; ok {
			*slot = tint.FromHex(hex)
		}
	}
}

// isDark reports whether a background wants light text on it. ContrastText
// already answers exactly that question for the chrome, so asking it here means
// an imported theme's dark flag and what the renderer does with the background
// cannot disagree.
func isDark(bg *tint.Color) bool {
	if bg == nil {
		return true
	}
	r, g, b, _ := ContrastText(bg).RGBA()
	return r > 0x7fff && g > 0x7fff && b > 0x7fff
}

// isHex reports whether a value is one of these formats' colour literals.
func isHex(v string) bool {
	return hexPattern.MatchString(strings.TrimSpace(strings.Trim(v, `"'`)))
}

// normalizeHex trims the quoting the toml formats add and restores the hash the
// line formats sometimes leave off.
func normalizeHex(v string) string {
	v = strings.TrimSpace(strings.Trim(strings.TrimSpace(v), `"'`))
	if !strings.HasPrefix(v, "#") {
		v = "#" + v
	}
	return strings.ToLower(v)
}
