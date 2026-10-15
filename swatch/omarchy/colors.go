package omarchy

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// This file is a port of bin/omarchy-theme-color, the resolver every Omarchy
// consumer shares, at basecamp/omarchy 246f33a. It reads a colors.toml the
// way that script's bash reads it, quirks included, and derives the missing
// keys with the same cascade, so a theme resolves here to the colours
// Omarchy gives every other app. testdata/ holds the script's own output for
// every shipped theme, and the parity test holds this port to it.

// MaxFileSize is the most bytes of a colors.toml this package reads. A
// shipped one is about 1 KiB. A larger file is not read at all.
const MaxFileSize = 64 << 10

// Resolve returns every key omarchy-theme-color --all prints for a
// colors.toml: the keys the file defines that pass Omarchy's checks, the
// aliases and the derived keys, as the strings the script prints. lightFile
// says whether a light.mode file sits beside the colors.toml.
//
// A value is the text the theme wrote, which need not be a colour: rgba()
// lists, gradients and bare words pass through as they are.
func Resolve(data []byte, lightFile bool) map[string]string {
	c := parseColors(data)
	resolveColors(c, lightFile)
	return c
}

// parseColors reads key = value lines as parse_colors_file does.
func parseColors(data []byte) map[string]string {
	c := map[string]string{}
	// bash's read loop never runs its body for a last line with no newline,
	// so that line is not read.
	end := bytes.LastIndexByte(data, '\n')
	if end < 0 {
		return c
	}
	for line := range strings.SplitSeq(string(data[:end]), "\n") {
		// bash drops NUL bytes from what read returns.
		line = strings.ReplaceAll(line, "\x00", "")
		key, value := splitLine(line)
		key = strings.Map(func(r rune) rune {
			if r == '"' || r == '\'' || r == ' ' {
				return -1
			}
			return r
		}, key)
		if key == "" || key[0] == '#' {
			continue
		}
		if strings.ContainsAny(value, `"'`) {
			_, value, _ = cutAny(value, `"'`)
			value, _, _ = cutAny(value, `"'`)
		} else {
			value = strings.Trim(value, " \t\n\v\f\r")
		}
		if !allIn(key, keyChars) || !allIn(value, valueChars) {
			continue
		}
		c[key] = value
	}
	return c
}

// splitLine splits a line as IFS='=' read -r key value does: the key is the
// text before the first '=', and the value is the rest, less one trailing
// '=' when the rest holds no other.
func splitLine(line string) (key, value string) {
	key, value, found := strings.Cut(line, "=")
	if !found {
		return key, ""
	}
	if strings.HasSuffix(value, "=") && strings.Count(value, "=") == 1 {
		value = value[:len(value)-1]
	}
	return key, value
}

// cutAny is strings.Cut at the first byte of value that is one of chars.
func cutAny(s, chars string) (before, after string, found bool) {
	i := strings.IndexAny(s, chars)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+1:], true
}

const (
	letters    = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	keyChars   = letters + "_-"
	valueChars = letters + "#(),._+/% -"
)

func allIn(s, set string) bool {
	for i := range len(s) {
		if strings.IndexByte(set, s[i]) < 0 {
			return false
		}
	}
	return true
}

// legacyPalette maps each canonical ground and text key to the short name
// older themes used.
var legacyPalette = [][2]string{
	{"background", "bg"},
	{"dark_background", "dark_bg"},
	{"darker_background", "darker_bg"},
	{"lighter_background", "lighter_bg"},
	{"foreground", "fg"},
	{"dark_foreground", "dark_fg"},
	{"light_foreground", "light_fg"},
	{"bright_foreground", "bright_fg"},
}

// semanticANSI is the ANSI slot behind each named colour.
var semanticANSI = [][2]string{
	{"red", "color1"},
	{"green", "color2"},
	{"yellow", "color3"},
	{"blue", "color4"},
	{"magenta", "color5"},
	{"cyan", "color6"},
	{"bright_red", "color9"},
	{"bright_green", "color10"},
	{"bright_yellow", "color11"},
	{"bright_blue", "color12"},
	{"bright_magenta", "color13"},
	{"bright_cyan", "color14"},
}

// ansiKeys is the key each ANSI slot takes its colour from, 0 to 15. It is
// also the mapping of Omarchy's kitty, ghostty and foot templates.
var ansiKeys = [16]string{
	"background", "red", "green", "yellow", "blue", "magenta", "cyan", "foreground",
	"muted", "bright_red", "bright_green", "bright_yellow", "bright_blue", "bright_magenta", "bright_cyan", "bright_foreground",
}

// resolveColors is resolve_theme_colors and resolve_theme_mode.
func resolveColors(c map[string]string, lightFile bool) {
	// alias sets key to fallback's value when key is empty. Like the
	// script, it creates key even when fallback is empty too.
	alias := func(key, fallback string) {
		if c[key] == "" {
			c[key] = c[fallback]
		}
	}
	// or is ${a:-b}.
	or := func(keys ...string) string {
		for _, k := range keys {
			if c[k] != "" {
				return c[k]
			}
		}
		return ""
	}
	set := func(key, value string) {
		if c[key] == "" {
			c[key] = value
		}
	}

	for _, p := range legacyPalette {
		alias(p[0], p[1])
	}
	set("background", c["color0"])
	set("foreground", c["color7"])
	if c["background"] != "" {
		c["color0"] = c["background"]
	}
	if c["foreground"] != "" {
		c["color7"] = c["foreground"]
	}
	for _, p := range semanticANSI {
		alias(p[0], p[1])
	}
	alias("magenta", "purple")
	alias("bright_magenta", "bright_purple")

	set("light_foreground", or("color7", "foreground"))
	set("bright_foreground", or("color15", "foreground"))
	c["cursor"] = c["bright_foreground"]
	set("lighter_background", or("color0", "background"))
	set("dark_foreground", or("color8", "foreground"))
	set("muted", or("color8", "dark_foreground"))
	set("selection", or("selection_background", "color8", "color0", "background"))
	set("selection_background", c["selection"])
	set("selection_foreground", c["bright_foreground"])
	set("orange", c["yellow"])
	if c["brown"] == "" {
		c["brown"] = mixColor(c["orange"], "#000000", 0.5)
	}

	if c["dark_background"] == "" {
		c["dark_background"] = mixColor(c["background"], "#000000", 0.25)
	}
	if c["darker_background"] == "" {
		c["darker_background"] = mixColor(c["background"], "#000000", 0.5)
	}
	for _, k := range []string{"red", "yellow", "green", "cyan", "blue", "magenta"} {
		if c["bright_"+k] == "" {
			c["bright_"+k] = mixColor(c[k], "#ffffff", 0.2)
		}
	}
	alias("purple", "magenta")
	alias("bright_purple", "bright_magenta")

	for i, k := range ansiKeys {
		alias("color"+strconv.Itoa(i), k)
	}
	for _, p := range legacyPalette {
		if c[p[0]] != "" {
			c[p[1]] = c[p[0]]
		}
	}

	if c["mode"] == "" {
		c["mode"] = c["theme_type"]
	}
	if c["mode"] == "" {
		switch {
		case lightFile:
			c["mode"] = "light"
		case isHex6(c["background"]):
			bg := c["background"][1:]
			lum := 0
			for i := 0; i < 6; i += 2 {
				v, _ := strconv.ParseUint(bg[i:i+2], 16, 8)
				lum += int(v)
			}
			if lum > 382 {
				c["mode"] = "light"
			} else {
				c["mode"] = "dark"
			}
		default:
			c["mode"] = "dark"
		}
	}
	c["theme_type"] = c["mode"]
}

func isHex6(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for i := 1; i < 7; i++ {
		if strings.IndexByte("0123456789abcdefABCDEF", s[i]) < 0 {
			return false
		}
	}
	return true
}

// mixColor is mix_color: start moved toward end by amount, both read as six
// hex digits after an optional '#'. It does what the script's awk does with
// anything else too, so a theme with a broken colour derives the same broken
// colour here: a missing digit reads as 0, any other non-digit as -1, and a
// negative channel prints as gawk prints it.
func mixColor(start, end string, amount float64) string {
	start = strings.TrimPrefix(start, "#")
	end = strings.TrimPrefix(end, "#")
	var b strings.Builder
	b.WriteByte('#')
	for i := 0; i < 6; i += 2 {
		s := float64(awkHexPair(start, i))
		e := float64(awkHexPair(end, i))
		v := int64(s*(1-amount) + e*amount + 0.5)
		if v < 0 {
			fmt.Fprintf(&b, "%x", uint64(v))
		} else {
			fmt.Fprintf(&b, "%02x", v)
		}
	}
	return b.String()
}

// awkHexPair is hex_pair_to_int: the two characters of hex at i as a number.
func awkHexPair(hex string, i int) int {
	return awkHexValue(hex, i)*16 + awkHexValue(hex, i+1)
}

// awkHexValue is hex_value of substr(hex, i+1, 1). substr past the end is
// the empty string, which gawk's index finds at position 1.
func awkHexValue(hex string, i int) int {
	if i >= len(hex) {
		return 0
	}
	return strings.IndexByte("0123456789abcdef", lower(hex[i]))
}

func lower(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + 'a' - 'A'
	}
	return c
}
