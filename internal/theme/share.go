package theme

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/color"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	tint "github.com/lrstanley/bubbletint/v2"
)

// A theme leaves the machine as the file a person would write: the id, the
// base it starts from, and only what differs from that base. A sparse file
// follows later fixes to its base, and it is short enough to paste into a
// chat message as a share string.

// SharePrefix starts every share string. The version is in the prefix so a
// later format can be told apart without guessing.
const SharePrefix = "tuios-theme:v1:"

// colourKeys are the JSON keys of a theme's colours, in the order
// themeColours lists them and a file is written in.
var colourKeys = [...]string{
	"fg", "bg", "selection_bg", "cursor",
	"black", "red", "green", "yellow", "blue", "purple", "cyan", "white",
	"bright_black", "bright_red", "bright_green", "bright_yellow",
	"bright_blue", "bright_purple", "bright_cyan", "bright_white",
}

// themeDoc is a theme as a file writes it. An empty colour or chrome field is
// left out of the file.
type themeDoc struct {
	ID, DisplayName, Base string
	Dark                  *bool
	Colours               [len(colourKeys)]string
	Chrome                chromeRaw
}

// encode writes the doc as JSON in a fixed key order, indented for a file or
// compact for a share string.
func (d themeDoc) encode(indent bool) []byte {
	type field struct {
		key   string
		value []byte
	}
	quote := func(v any) []byte { b, _ := json.Marshal(v); return b }
	var fields []field
	add := func(key string, v any) { fields = append(fields, field{key, quote(v)}) }
	add("id", d.ID)
	if d.DisplayName != "" && d.DisplayName != d.ID {
		add("display_name", d.DisplayName)
	}
	if d.Base != "" {
		add("base", d.Base)
	}
	if d.Dark != nil {
		add("dark", *d.Dark)
	}
	for i, c := range d.Colours {
		if c != "" {
			add(colourKeys[i], c)
		}
	}
	chrome := encodeChrome(d.Chrome, quote)
	if chrome != nil {
		fields = append(fields, field{"chrome", chrome})
	}

	var b bytes.Buffer
	b.WriteString("{")
	for i, f := range fields {
		if i > 0 {
			b.WriteString(",")
		}
		b.Write(quote(f.key))
		b.WriteString(":")
		b.Write(f.value)
	}
	b.WriteString("}")
	var out bytes.Buffer
	if indent {
		if err := json.Indent(&out, b.Bytes(), "", "  "); err != nil {
			return append(b.Bytes(), '\n')
		}
	} else if err := json.Compact(&out, b.Bytes()); err != nil {
		return append(b.Bytes(), '\n')
	}
	out.WriteByte('\n')
	return out.Bytes()
}

// encodeChrome writes the chrome fields as one JSON object on one line, in
// role order. A role named group.field goes in the nested object group, which
// sits where the group's first role would. Nil when no field is set.
func encodeChrome(raw chromeRaw, quote func(any) []byte) []byte {
	var b bytes.Buffer
	open := "" // the nested object being written, or "" at the top
	top, inGroup := 0, 0
	for r := RoleAccent; r <= roleLastSettable; r++ {
		if raw[r] == "" {
			continue
		}
		group, field, nested := strings.Cut(r.String(), ".")
		if !nested {
			group, field = "", r.String()
		}
		if group != open {
			if open != "" {
				b.WriteString("}")
			}
			open = group
			if group != "" {
				if top > 0 {
					b.WriteString(", ")
				}
				top++
				b.Write(quote(group))
				b.WriteString(": {")
				inGroup = 0
			}
		}
		if group == "" {
			if top > 0 {
				b.WriteString(", ")
			}
			top++
		} else {
			if inGroup > 0 {
				b.WriteString(", ")
			}
			inGroup++
		}
		b.Write(quote(field))
		b.WriteString(": ")
		b.Write(quote(raw[r]))
	}
	if top == 0 {
		return nil
	}
	if open != "" {
		b.WriteString("}")
	}
	return []byte("{" + b.String() + "}")
}

// tintHex is a theme colour as #rrggbb, or "" for an unset one.
func tintHex(c *tint.Color) string {
	if c == nil {
		return ""
	}
	return c.Hex()
}

// fullDoc is every colour of t and the chrome fields raw, with no base.
func fullDoc(t *tint.Tint, chrome chromeRaw) themeDoc {
	dark := t.Dark
	d := themeDoc{ID: t.ID, DisplayName: t.DisplayName, Dark: &dark, Chrome: chrome}
	for i, c := range themeColours(t) {
		d.Colours[i] = tintHex(*c)
	}
	return d
}

// exportDoc builds the doc for a theme id. A theme from your themes directory
// is written against the nearest built-in it starts from, with only what
// differs, so a file based on another file of yours still loads on a machine
// that does not have that file. A built-in theme, or any theme with full set,
// is written with every colour and no base.
func exportDoc(id string, full bool) (themeDoc, error) {
	EnsureRegistry()
	ReloadCustomThemes()
	themeBases.RLock()
	r, custom := themeBases.customs[id]
	themeBases.RUnlock()
	if !custom {
		t, ok := tint.GetTint(id)
		if !ok || t == nil {
			return themeDoc{}, fmt.Errorf("%q is not a theme. Run tuios list-themes for the names", id)
		}
		return fullDoc(t, builtinTheme(id, t).chrome), nil
	}
	if full {
		return fullDoc(r.tint, r.chrome), nil
	}
	anchor, base, ok := builtinAnchor(id)
	if !ok {
		return fullDoc(r.tint, r.chrome), nil
	}
	d := themeDoc{ID: r.tint.ID, DisplayName: r.tint.DisplayName, Base: base}
	if r.tint.Dark != anchor.tint.Dark {
		dark := r.tint.Dark
		d.Dark = &dark
	}
	from := themeColours(anchor.tint)
	for i, c := range themeColours(r.tint) {
		if h := tintHex(*c); h != tintHex(*from[i]) {
			d.Colours[i] = h
		}
	}
	for role := RoleAccent; role <= roleLastSettable; role++ {
		if r.chrome[role] != anchor.chrome[role] {
			d.Chrome[role] = r.chrome[role]
		}
	}
	return d, nil
}

// builtinAnchor walks the bases of the theme file id to the first built-in
// theme. It reports false for a file with no base, or a base that is not
// there: such a file is written in full.
func builtinAnchor(id string) (resolvedTheme, string, bool) {
	themeBases.RLock()
	defer themeBases.RUnlock()
	seen := map[string]bool{id: true}
	b := themeBases.customs[id].base
	for b != "" {
		if c, ok := themeBases.customs[b]; ok && !seen[b] {
			seen[b] = true
			b = c.base
			continue
		}
		// A file that takes a built-in's id and names it as its base starts
		// from the built-in as shipped.
		if r, ok := themeBases.builtins[b]; ok {
			return r, b, true
		}
		return resolvedTheme{}, "", false
	}
	return resolvedTheme{}, "", false
}

// ExportTheme returns a theme as an indented theme file. See exportDoc for
// what full changes.
func ExportTheme(id string, full bool) ([]byte, error) {
	d, err := exportDoc(id, full)
	if err != nil {
		return nil, err
	}
	return d.encode(true), nil
}

// ShareString returns a theme as one line: SharePrefix and the compact sparse
// file in unpadded base64url.
func ShareString(id string) (string, error) {
	d, err := exportDoc(id, false)
	if err != nil {
		return "", err
	}
	body := bytes.TrimSpace(d.encode(false))
	return SharePrefix + base64.RawURLEncoding.EncodeToString(body), nil
}

// ExportBase16 returns a theme as a base16 scheme in YAML, the tinted-theming
// form with a palette table. base00 is the background, as every base16 editor
// theme reads it. The accents and the two raised grounds come from the
// theme's chrome where it names them. A ground it does not name steps from
// the background towards the foreground.
func ExportBase16(id string) ([]byte, error) {
	EnsureRegistry()
	ReloadCustomThemes()
	t, ok := tint.GetTint(id)
	if !ok || t == nil {
		return nil, fmt.Errorf("%q is not a theme. Run tuios list-themes for the names", id)
	}
	chromeRegistry.RLock()
	c := chromeRegistry.byID[id]
	chromeRegistry.RUnlock()
	named := func(pick func(*Chrome) color.Color, fallback *tint.Color) color.Color {
		if c != nil {
			if v := pick(c); v != nil {
				return v
			}
		}
		return AsColor(fallback)
	}
	// A ground the theme names only when it names a surface: without one the
	// ramp is CharmTone's, which says nothing about this theme.
	bg, fg := AsColor(t.Bg), AsColor(t.Fg)
	ground := func(pick func(*Chrome) color.Color, step float64) color.Color {
		if c != nil && c.Surface != nil {
			return pick(c)
		}
		return overlay.MixColors(bg, fg, step)
	}
	red, yellow := AsColor(t.Red), AsColor(t.Yellow)
	rows := [16]color.Color{
		bg,
		ground(func(c *Chrome) color.Color { return c.Surface }, 0.08),
		ground(func(c *Chrome) color.Color { return c.Card }, 0.16),
		AsColor(t.BrightBlack),
		overlay.MixColors(bg, fg, 0.6),
		fg,
		AsColor(t.White),
		AsColor(t.BrightWhite),
		named(func(c *Chrome) color.Color { return c.Error }, t.Red),
		overlay.MixColors(red, yellow, 0.5),
		named(func(c *Chrome) color.Color { return c.Warning }, t.Yellow),
		named(func(c *Chrome) color.Color { return c.Success }, t.BrightGreen),
		named(func(c *Chrome) color.Color { return c.AccentBright }, t.BrightCyan),
		named(func(c *Chrome) color.Color { return c.Accent }, t.BrightBlue),
		AsColor(t.Purple),
		overlay.MixColors(red, bg, 0.4),
	}
	var b strings.Builder
	name, _ := json.Marshal(t.DisplayName)
	fmt.Fprintf(&b, "system: \"base16\"\nname: %s\nauthor: \"\"\nvariant: %q\npalette:\n", name, variantOf(t.Dark))
	for i, col := range rows {
		fmt.Fprintf(&b, "  base%02X: \"%s\"\n", i, ColorToString(col))
	}
	return []byte(b.String()), nil
}

// variantOf is base16's word for a dark or light scheme.
func variantOf(dark bool) string {
	if dark {
		return "dark"
	}
	return "light"
}
