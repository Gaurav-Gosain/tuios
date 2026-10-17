package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/pelletier/go-toml/v2"

	"github.com/Gaurav-Gosain/tuios/internal/atomicfile"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// Looks.
//
// A look is a small TOML file that sets how tuios looks: theme, glyph set,
// borders, gaps, dock and rail style. People share them, the tuios.dev gallery
// publishes them, and `tuios look apply` installs one. Strangers write them, so
// a look is data only. It may set the options in lookKeys and nothing else:
// no hooks, commands, shells, keybindings, paths, hosts or daemon settings.
// Every other key is left out and named, and every value goes through the
// same check set-config uses.
//
// # Format, version 1
//
//	look = 1                       # required: the format version
//	name = "Tokyo Night, powerline" # optional, shown in the preview
//	author = "someone"              # optional
//
//	[appearance]                    # any option in lookKeys, in its own table
//	theme = "tokyo"
//	border_style = "rounded"
//	gap = 1
//
//	[appearance.sidebar]
//	row_style = "pill"
//
//	[theme]                         # optional: a theme, the keys of a theme JSON file
//	id = "tokyo"
//	base = "tokyonight"
//	accent = "#7aa2f7"
//
//	[glyphs]                        # optional: a glyph set, the keys of a glyph set JSON file
//	id = "tokyo"
//	close = "x"
//
// A theme or glyph set the look carries is installed under the id with
// LookAssetPrefix in front (look-tokyo), so it never replaces a file of the
// person's own. appearance.theme and appearance.glyphs follow the rename.
//
// # Where it goes
//
// The look is written to config.d/look.toml, its own layer (see include.go).
// config.toml is never edited, so `tuios look remove` deletes that one file and
// the installed assets, and the config is what it was. config.toml still wins
// over config.d, so a key config.toml sets hides the same key in the look. The
// plan names those keys. A settings change of a key the look sets goes to
// config.toml, never to look.toml (see layerWriter.target), so a later look
// apply or remove does not undo it.

// LookVersion is the look format version this build reads.
const LookVersion = 1

// LookFileName is the look's file in config.d.
const LookFileName = "look.toml"

// LookAssetPrefix starts the id of every theme and glyph set a look installs.
const LookAssetPrefix = "look-"

// MaxLookBytes caps a look file.
const MaxLookBytes = 64 << 10

// maxLookString caps one string value or name in a look.
const maxLookString = 512

// lookKeys are the options a look may set: how tuios looks, and nothing that
// runs a program, names a file, reaches the network or changes what a key
// does. The list is closed on purpose. An option added to the registry later
// is not a look key until it is listed here.
var lookKeys = []string{
	// Theme, glyphs and colour.
	"appearance.theme",
	"appearance.theme_light",
	"appearance.glyphs",
	"appearance.theme_transition",
	"appearance.theme_transition_duration",
	"appearance.background",
	"appearance.desktop_background",
	"appearance.pane_background",
	"appearance.window_chrome_background",
	"appearance.dim_unfocused",
	"appearance.dim_foreground",
	"appearance.dim_multifocus",
	"appearance.modal_dim",
	"appearance.session_colors",
	// Borders and the window frame.
	"appearance.border_style",
	"appearance.border_focused_color",
	"appearance.border_unfocused_color",
	"appearance.border_focused_gradient",
	"appearance.border_gradient_angle",
	"appearance.session_border",
	"appearance.shared_borders",
	"appearance.gap",
	"appearance.panel_padding",
	"appearance.zen_mode",
	"appearance.hide_window_buttons",
	"appearance.window_button_position",
	"appearance.window_button_style",
	"appearance.window_title_align",
	"appearance.window_title_format",
	"appearance.window_title_position",
	"appearance.window_title_style",
	"appearance.hide_scrollbar",
	"appearance.drop_preview",
	// Layout: where the panes go and how big they are. These place panes and
	// size them, and do nothing else.
	"startup.tiled",
	"startup.layout",
	"appearance.master_position",
	"appearance.master_ratio",
	"appearance.scroll_column_width",
	"appearance.zoom_size",
	"appearance.zoom_max_width",
	// Motion.
	"appearance.motion",
	"appearance.animation_speed",
	"appearance.zoom_animation",
	// The dock.
	"appearance.dockbar_position",
	"appearance.dock_background",
	"appearance.dock_compact",
	"appearance.dock_pill_caps",
	"appearance.dock_prefix_indicator",
	"appearance.dock_mode_icon_terminal",
	"appearance.dock_mode_icon_tiling",
	"appearance.dock_mode_icon_window",
	"appearance.dock_workspace_tabs",
	"appearance.dock_workspace_tab_format",
	"appearance.dock_workspace_label_max",
	"appearance.show_clock",
	"appearance.show_cpu",
	"appearance.show_ram",
	"appearance.clock_format",
	"dock.clock.format",
	// The rail.
	"appearance.sidebar.enabled",
	"appearance.sidebar.position",
	"appearance.sidebar.width",
	"appearance.sidebar.background",
	"appearance.sidebar.density",
	"appearance.sidebar.row_style",
	"appearance.sidebar.header_style",
	"appearance.sidebar.header_case",
	"appearance.sidebar.show_counts",
	"appearance.sidebar.show_glyphs",
	"appearance.sidebar.show_numbers",
	"appearance.sidebar.file_icons",
	"appearance.sidebar.file_icon_colors",
	"appearance.sidebar.marquee",
	// Each item is checked on its own: see lookItemRules.
	"appearance.sidebar.sections",
	// The scrollbar.
	"appearance.scrollbar.style",
	"appearance.scrollbar.thumb",
	"appearance.scrollbar.track",
	"appearance.scrollbar.tint",
	// Selection and search colours.
	"appearance.selection.bg",
	"appearance.selection.fg",
	"appearance.selection.bold",
	"appearance.selection.cursor_bg",
	"appearance.selection.cursor_fg",
	"appearance.selection.match_bg",
	"appearance.selection.match_fg",
	"appearance.selection.search_bg",
	"appearance.selection.search_fg",
	"appearance.selection.flash",
	"appearance.selection.flash_color",
	"appearance.selection.flash_ms",
	"appearance.selection.flash_style",
	// The focus spotlight.
	"spotlight.enabled",
	"spotlight.dim",
	"spotlight.edge",
	"spotlight.radius",
}

// LookKeys returns the options a look may set, sorted.
func LookKeys() []string {
	out := make([]string, 0, len(lookKeys)+len(lookFileKeys))
	for _, k := range lookKeys {
		if IsLookKey(k) {
			out = append(out, k)
		}
	}
	for k := range lookFileKeys {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// IsLookKey reports whether a look may set the option at path: it is on the
// list, a registered option, and does not reach the host, or it is one of the
// dock keys the registry does not carry (see lookFileKeys).
func IsLookKey(path string) bool {
	if _, ok := lookFileKeys[path]; ok {
		return true
	}
	if !slices.Contains(lookKeys, path) || ReachesHost(path) {
		return false
	}
	_, ok := LookupOption(path)
	return ok
}

// hostReaching are the options inside the look and appearance sections that
// run a program, name a file, or write the host clipboard. Neither a look nor
// a browser page (internal/learn) may set one. The look list above is closed
// already, so for a look this is a second lock on the same door.
var hostReaching = map[string]bool{
	"appearance.link_opener":            true,
	"appearance.preferred_shell":        true,
	"appearance.selection.copy_command": true,
	"appearance.selection.osc52_write":  true,
	"appearance.sidebar.editor":         true,
	"appearance.sidebar.file_delete":    true,
}

// ReachesHost reports whether the option at path runs a program, names a file
// or writes the host clipboard.
func ReachesHost(path string) bool { return hostReaching[path] }

// LookKeyInfo is one look key as the shared list publishes it.
type LookKeyInfo struct {
	Path        string   `json:"path"`
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Default     string   `json:"default"`
	Accepted    []string `json:"accepted,omitempty"`
	Min         int      `json:"min,omitempty"`
	Max         int      `json:"max,omitempty"`
	Color       bool     `json:"color,omitempty"`
	// Items are the names one item may be, for a list, or for a string of
	// items joined by Split. An item may carry ":share" after the name.
	Items    []string `json:"items,omitempty"`
	Split    string   `json:"split,omitempty"`
	MaxItems int      `json:"max_items,omitempty"`
	// MaxBytes caps a text value, or one list item, below the 512 bytes
	// every text value has.
	MaxBytes int `json:"max_bytes,omitempty"`
}

// LookKeyList is the look key list the tuios.dev gallery vendors, so the
// gallery and tuios keep the same keys.
type LookKeyList struct {
	Look int           `json:"look"`
	Keys []LookKeyInfo `json:"keys"`
}

// LookKeysJSON is the look key list as indented JSON, ending in a newline.
func LookKeysJSON() ([]byte, error) {
	list := LookKeyList{Look: LookVersion}
	for _, k := range LookKeys() {
		if spec, ok := lookFileKeys[k]; ok {
			info := LookKeyInfo{
				Path: k, Type: spec.Type, Description: spec.Description, Default: spec.Default,
				Color: spec.Color, MaxItems: spec.MaxItems,
			}
			if spec.MaxBytes < maxLookString {
				info.MaxBytes = spec.MaxBytes
			}
			if spec.Type == LookList {
				info.Items = spec.Accepted
			} else {
				info.Accepted = spec.Accepted
			}
			list.Keys = append(list.Keys, info)
			continue
		}
		opt, _ := LookupOption(k)
		info := LookKeyInfo{
			Path: k, Type: opt.Type, Description: opt.Description, Default: opt.Default,
			Accepted: opt.Accepted,
			Min:      opt.Min, Max: opt.Max, Color: opt.Color,
		}
		if items, ok := lookItemKeys(k); ok {
			info.Items, info.Split = items, ","
		}
		list.Keys = append(list.Keys, info)
	}
	out, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// Look is a parsed and checked look.
type Look struct {
	Name   string
	Author string
	// Settings are the options the look sets, sorted by path, with the theme
	// and glyph set ids already renamed to their installed ids.
	Settings []LookSetting
	// Theme and Glyphs are the files to install, or nil.
	Theme  *LookAsset
	Glyphs *LookAsset
	// Left are the keys the look holds that a look may not set, sorted. A
	// table none of whose keys a look may set is named as the table.
	Left []string
	// Notes say what the check changed, such as a glyph that was too wide.
	Notes []string
}

// LookSetting is one option a look sets.
type LookSetting struct {
	Path  string
	Value any // bool, int64, string or []string, as TOML holds it
}

// Text is the value as set-config would take it.
func (s LookSetting) Text() string { return lookValueText(s.Value) }

// LookAsset is a theme or glyph set file a look installs.
type LookAsset struct {
	ID   string // the installed id, with LookAssetPrefix
	Data []byte // the checked file
}

// lookMeta are the top-level keys a look file has besides its options.
var lookMeta = map[string]bool{"look": true, "name": true, "author": true, "description": true, "theme": true, "glyphs": true}

// ParseLook reads and checks a look file. A key a look may not set is left out
// and named in Left. A value that fails its check fails the whole look.
func ParseLook(data []byte) (*Look, error) {
	if len(data) > MaxLookBytes {
		return nil, fmt.Errorf("the look is larger than %d KiB", MaxLookBytes>>10)
	}
	raw := map[string]any{}
	if err := toml.Unmarshal(data, &raw); err != nil {
		// The error can quote a key, and a stranger names the keys.
		return nil, fmt.Errorf("the look is not valid TOML: %s", lookOneLine(err.Error()))
	}
	switch v := raw["look"].(type) {
	case nil:
		return nil, errors.New("the file has no look = 1 line, so it is not a look")
	case int64:
		if v > LookVersion {
			return nil, fmt.Errorf("the look is version %d. This tuios reads version %d. Update tuios", v, LookVersion)
		}
		if v != LookVersion {
			return nil, fmt.Errorf("look = %d is not a look version. Use look = %d", v, LookVersion)
		}
	default:
		return nil, fmt.Errorf("look must be a whole number. Use look = %d", LookVersion)
	}
	lk := &Look{}
	var err error
	if lk.Name, err = lookText(raw, "name"); err != nil {
		return nil, err
	}
	if lk.Author, err = lookText(raw, "author"); err != nil {
		return nil, err
	}
	if _, err = lookText(raw, "description"); err != nil {
		return nil, err
	}

	themeOwn, err := lk.readAsset(raw, "theme", false)
	if err != nil {
		return nil, err
	}
	glyphOwn, err := lk.readAsset(raw, "glyphs", true)
	if err != nil {
		return nil, err
	}

	options := map[string]any{}
	for k, v := range raw {
		if !lookMeta[k] {
			options[k] = v
		}
	}
	values := map[string]any{}
	if err := lk.walk(options, nil, values); err != nil {
		return nil, err
	}
	slices.Sort(lk.Left)

	if err := lk.bindAsset(values, "appearance.theme", themeOwn, lk.Theme); err != nil {
		return nil, err
	}
	if err := lk.bindAsset(values, "appearance.glyphs", glyphOwn, lk.Glyphs); err != nil {
		return nil, err
	}

	scratch := DefaultConfig()
	for _, path := range lookSortedKeys(values) {
		v := values[path]
		s := LookSetting{Path: path, Value: v}
		installed := (lk.Theme != nil && path == "appearance.theme" && v == lk.Theme.ID) ||
			(lk.Glyphs != nil && path == "appearance.glyphs" && v == lk.Glyphs.ID)
		switch _, file := lookFileKeys[path]; {
		case file:
			if err := checkLookFileValue(path, v); err != nil {
				return nil, err
			}
		case !installed:
			if err := SetOptionValue(scratch, path, s.Text()); err != nil {
				return nil, err
			}
		}
		lk.Settings = append(lk.Settings, s)
	}

	// The fragment as a whole goes through the validator a config load uses,
	// for the checks that look at more than one key.
	frag, err := lk.fragment()
	if err != nil {
		return nil, err
	}
	cfg, err := ParseUserConfig(frag)
	if err != nil {
		return nil, err
	}
	if res := ValidateConfig(cfg); res.HasErrors() {
		e := res.Errors[0]
		return nil, fmt.Errorf("%s: %s", e.Key, e.Message)
	}
	return lk, nil
}

// lookText reads an optional one-line string at key.
func lookText(raw map[string]any, key string) (string, error) {
	v, ok := raw[key]
	if !ok {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s must be text", key)
	}
	if len(s) > maxLookString || hasControlChar(s) {
		return "", fmt.Errorf("%s must be one line of at most %d bytes", key, maxLookString)
	}
	return s, nil
}

// readAsset checks the [theme] or [glyphs] table and returns the id the look
// gave it.
func (lk *Look) readAsset(raw map[string]any, key string, glyphs bool) (string, error) {
	v, ok := raw[key]
	if !ok {
		return "", nil
	}
	table, ok := v.(map[string]any)
	if !ok {
		return "", fmt.Errorf("%s must be a table, such as [%s]", key, key)
	}
	own, _ := table["id"].(string)
	if own == "" {
		return "", fmt.Errorf("[%s] needs an id", key)
	}
	own = strings.TrimPrefix(own, LookAssetPrefix)
	id := LookAssetPrefix + own
	if !theme.ValidThemeID(own) || !theme.ValidThemeID(id) {
		return "", fmt.Errorf("[%s] id %q cannot be a file name. Use a-z, 0-9, '-', '_' and '.'", key, own)
	}
	data, err := json.Marshal(table)
	if err != nil {
		return "", fmt.Errorf("[%s]: %w", key, err)
	}
	asset := &LookAsset{ID: id}
	if glyphs {
		out, notes, err := theme.CheckLookGlyphs(id, data)
		if err != nil {
			return "", fmt.Errorf("[%s]: %w", key, err)
		}
		asset.Data = out
		lk.Notes = append(lk.Notes, notes...)
		lk.Glyphs = asset
	} else {
		out, err := theme.CheckLookTheme(id, data)
		if err != nil {
			return "", fmt.Errorf("[%s]: %w", key, err)
		}
		asset.Data = out
		lk.Theme = asset
	}
	return own, nil
}

// bindAsset points the option at the installed asset: a look that carries a
// theme and leaves appearance.theme unset uses it, and one that names it by
// its own id names the installed id.
func (lk *Look) bindAsset(values map[string]any, path, own string, asset *LookAsset) error {
	if asset == nil {
		return nil
	}
	v, set := values[path]
	switch {
	case !set:
		values[path] = asset.ID
	case v == own || v == asset.ID:
		values[path] = asset.ID
	default:
		return fmt.Errorf("the look carries %s %q, but %s names %q. Remove %s or set it to %q", path[strings.LastIndex(path, ".")+1:], own, path, lookValueText(v), path, own)
	}
	return nil
}

// walk sorts the look's keys into values, the ones a look may set, and
// lk.Left. A list checked item by item keeps its allowed items, and each
// other item goes to lk.Left.
func (lk *Look) walk(m map[string]any, prefix []string, values map[string]any) error {
	for _, k := range lookSortedKeys(m) {
		path := append(slices.Clone(prefix), k)
		dotted := strings.Join(path, ".")
		v := m[k]
		if sub, ok := v.(map[string]any); ok {
			if !lookTableHasKey(dotted) {
				lk.Left = append(lk.Left, lookKeyShown(dotted))
				continue
			}
			if err := lk.walk(sub, path, values); err != nil {
				return err
			}
			continue
		}
		if !IsLookKey(dotted) {
			lk.Left = append(lk.Left, lookKeyShown(dotted))
			continue
		}
		kept, dropped, err := lookItems(dotted, v)
		if err != nil {
			return err
		}
		for _, item := range dropped {
			lk.Left = append(lk.Left, lookItemShown(dotted, item))
		}
		if kept == nil {
			lk.Left = append(lk.Left, lookKeyShown(dotted))
			continue
		}
		values[dotted] = kept
	}
	return nil
}

// lookKeyShown is a key name as the plan prints it. A stranger names the keys,
// and a quoted TOML key may hold an escape sequence, which would reach the
// terminal (an OSC 52 clipboard write, say) when the plan lists the keys it
// leaves out. A name with anything but graphic characters is printed quoted.
func lookKeyShown(key string) string {
	if strings.IndexFunc(key, func(r rune) bool { return !unicode.IsGraphic(r) }) < 0 {
		return key
	}
	return strconv.QuoteToGraphic(key)
}

// lookTableHasKey reports whether some look key is inside the table at path.
func lookTableHasKey(path string) bool {
	for _, k := range LookKeys() {
		if strings.HasPrefix(k, path+".") {
			return true
		}
	}
	return false
}

// lookValueText is a TOML value as set-config takes it. A value of a type no
// look key has is returned in a form no check accepts.
func lookValueText(v any) string {
	switch v := v.(type) {
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case []string:
		quoted := make([]string, len(v))
		for i, s := range v {
			quoted[i] = strconv.Quote(s)
		}
		return "[" + strings.Join(quoted, ", ") + "]"
	case []any:
		items := make([]string, 0, len(v))
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				return fmt.Sprintf("%v (a %T)", v, v)
			}
			items = append(items, s)
		}
		return lookValueText(items)
	default:
		return fmt.Sprintf("%v (a %T)", v, v)
	}
}

// fragment is the look's settings as the TOML of config.d/look.toml, without
// the header.
func (lk *Look) fragment() ([]byte, error) {
	root := map[string]any{}
	for _, s := range lk.Settings {
		switch v := s.Value.(type) {
		case string:
			if len(v) > maxLookString || hasControlChar(v) {
				return nil, fmt.Errorf("%s must be one line of at most %d bytes", s.Path, maxLookString)
			}
		case []string:
			for _, item := range v {
				if len(item) > maxLookString || hasControlChar(item) {
					return nil, fmt.Errorf("each item of %s must be one line of at most %d bytes", s.Path, maxLookString)
				}
			}
		case bool, int64:
		default:
			return nil, fmt.Errorf("%s: %v is not a value this option takes", s.Path, v)
		}
		parts := strings.Split(s.Path, ".")
		t := root
		for _, p := range parts[:len(parts)-1] {
			next, ok := t[p].(map[string]any)
			if !ok {
				next = map[string]any{}
				t[p] = next
			}
			t = next
		}
		t[parts[len(parts)-1]] = s.Value
	}
	return toml.Marshal(root)
}

// lookOneLine replaces each control character and bidi control in s with a
// space.
func lookOneLine(s string) string {
	return strings.Map(func(r rune) rune {
		if theme.IsUnsafeRune(r) {
			return ' '
		}
		return r
	}, s)
}

func hasControlChar(s string) bool { return strings.IndexFunc(s, theme.IsUnsafeRune) >= 0 }

func lookSortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// InstalledLook is the look config.d/look.toml holds, read from its header.
type InstalledLook struct {
	Path   string
	Name   string
	Source string
	Theme  string // installed theme id, or empty
	Glyphs string // installed glyph set id, or empty
	// Settings are the options the file sets.
	Settings []LookSetting
}

// Header lines of config.d/look.toml. tuios reads them back to know what to
// remove. lookHeadMark starts the first line, and a file without it is the
// person's own: tuios never replaces or deletes it.
const (
	lookHeadMark   = "# A tuios look. "
	lookHeadName   = "# look: "
	lookHeadSource = "# source: "
	lookHeadTheme  = "# theme: "
	lookHeadGlyphs = "# glyphs: "
)

// LookPath is the look file for the config at mainPath.
func LookPath(mainPath string) string {
	return filepath.Join(filepath.Dir(absPath(mainPath)), DropInDirName, LookFileName)
}

// ReadInstalledLook reads config.d/look.toml. It returns nil and no error when
// there is no look.
func ReadInstalledLook(mainPath string) (*InstalledLook, error) {
	path := LookPath(mainPath)
	data, err := os.ReadFile(path) //nolint:gosec // the user's own config.d
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(string(data), lookHeadMark) {
		return nil, fmt.Errorf("%s is your own file, not a look tuios applied. tuios does not change it. Rename it to use tuios look", filepath.Join(DropInDirName, LookFileName))
	}
	il := &InstalledLook{Path: path}
	for line := range strings.Lines(string(data)) {
		line = strings.TrimRight(line, "\r\n")
		if !strings.HasPrefix(line, "#") {
			break
		}
		if v, ok := strings.CutPrefix(line, lookHeadName); ok {
			il.Name = v
		} else if v, ok := strings.CutPrefix(line, lookHeadSource); ok {
			il.Source = v
		} else if v, ok := strings.CutPrefix(line, lookHeadTheme); ok && isLookAssetID(v) {
			il.Theme = v
		} else if v, ok := strings.CutPrefix(line, lookHeadGlyphs); ok && isLookAssetID(v) {
			il.Glyphs = v
		}
	}
	values, err := parseLayer(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", path, err)
	}
	var walk func(m map[string]any, prefix string)
	walk = func(m map[string]any, prefix string) {
		for _, k := range lookSortedKeys(m) {
			if sub, ok := m[k].(map[string]any); ok {
				walk(sub, prefix+k+".")
				continue
			}
			il.Settings = append(il.Settings, LookSetting{Path: prefix + k, Value: m[k]})
		}
	}
	walk(values, "")
	return il, nil
}

// isLookAssetID reports whether id names an asset a look installed. Only such
// an id is ever removed.
func isLookAssetID(id string) bool {
	return strings.HasPrefix(id, LookAssetPrefix) && theme.ValidThemeID(id)
}

// render is config.d/look.toml for lk, installed from source.
func (lk *Look) render(source string) ([]byte, error) {
	frag, err := lk.fragment()
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	b.WriteString(lookHeadMark + "\"tuios look apply\" wrote this file.\n")
	b.WriteString("# \"tuios look remove\" deletes it. The next apply replaces it.\n")
	b.WriteString("# config.toml wins over this file. Change settings there.\n")
	if lk.Name != "" {
		b.WriteString(lookHeadName + lookOneLine(lk.Name) + "\n")
	}
	if source != "" {
		b.WriteString(lookHeadSource + lookOneLine(source) + "\n")
	}
	if lk.Theme != nil {
		b.WriteString(lookHeadTheme + lk.Theme.ID + "\n")
	}
	if lk.Glyphs != nil {
		b.WriteString(lookHeadGlyphs + lk.Glyphs.ID + "\n")
	}
	b.WriteString("\n")
	b.Write(frag)
	return []byte(b.String()), nil
}

// LookChange is one option whose value in force changes.
type LookChange struct {
	Path, From, To string
}

// LookShadow is a look key that a later file sets, so the look does not
// change it.
type LookShadow struct {
	Path string
	File string // the file that sets it, as a person reads it
}

// LookPlan is what an apply or a remove will do.
type LookPlan struct {
	Main     string
	Look     *Look          // nil for a remove
	Source   string         // where the look came from
	Current  *InstalledLook // the look installed now, or nil
	Changes  []LookChange
	Shadowed []LookShadow
}

// PlanLook works out what applying lk to the config at mainPath changes. It
// writes nothing. A config tuios cannot write is an error here, before the
// question.
func PlanLook(mainPath string, lk *Look, source string) (*LookPlan, error) {
	return planLook(mainPath, lk, source)
}

// PlanLookRemove works out what removing the installed look changes. It
// returns a plan with no Current when there is no look.
func PlanLookRemove(mainPath string) (*LookPlan, error) {
	return planLook(mainPath, nil, "")
}

func planLook(mainPath string, lk *Look, source string) (*LookPlan, error) {
	lc, err := loadLayered(mainPath, true)
	if err != nil {
		return nil, err
	}
	cur, err := ReadInstalledLook(lc.Main)
	if err != nil {
		return nil, err
	}
	p := &LookPlan{Main: lc.Main, Look: lk, Source: source, Current: cur}
	if lk == nil && cur == nil {
		return p, nil
	}
	if !fileWritable(LookPath(lc.Main)) {
		return nil, fmt.Errorf("tuios cannot write %s. The config is read-only, so tuios cannot change the look", displayPath(lc.Main, filepath.Dir(LookPath(lc.Main))))
	}

	// A theme or glyph set of the person's own may already have the id the
	// look installs. Only an asset the installed look recorded is replaced.
	if lk != nil {
		for _, a := range []struct {
			asset  *LookAsset
			glyphs bool
			kind   string
		}{{lk.Theme, false, "theme"}, {lk.Glyphs, true, "glyph set"}} {
			if a.asset == nil || cur != nil && (a.asset.ID == cur.Theme && !a.glyphs || a.asset.ID == cur.Glyphs && a.glyphs) {
				continue
			}
			if theme.LookAssetThere(a.glyphs, a.asset.ID) {
				return nil, fmt.Errorf("a %s named %s is there already, and no look installed it. tuios does not replace it. Rename it to apply this look", a.kind, a.asset.ID)
			}
		}
	}

	next := &LayeredConfig{Main: lc.Main, Layered: true}
	lookPath := LookPath(lc.Main)
	var lookLayer *ConfigLayer
	if lk != nil {
		frag, err := lk.fragment()
		if err != nil {
			return nil, err
		}
		values, err := parseLayer(frag)
		if err != nil {
			return nil, err
		}
		lookLayer = &ConfigLayer{Path: lookPath, Kind: LayerDropIn, Data: frag, Values: values}
	}
	placed := false
	for _, l := range lc.Layers {
		if l.isLook() {
			continue
		}
		if lookLayer != nil && !placed && (l.Kind == LayerMain || l.Kind == LayerDropIn && l.Path > lookPath) {
			next.Layers = append(next.Layers, *lookLayer)
			placed = true
		}
		next.Layers = append(next.Layers, l)
	}
	before, err := parseLayered(lc)
	if err != nil {
		return nil, err
	}
	after, err := parseLayered(next)
	if err != nil {
		return nil, err
	}
	for _, opt := range Options() {
		from, _ := GetOptionValue(before, opt.Path)
		to, _ := GetOptionValue(after, opt.Path)
		if from != to {
			p.Changes = append(p.Changes, LookChange{Path: opt.Path, From: from, To: to})
		}
	}
	for _, path := range lookSortedKeys(lookFileKeys) {
		from, to := lookFileValueText(before, path), lookFileValueText(after, path)
		if from != to {
			p.Changes = append(p.Changes, LookChange{Path: path, From: from, To: to})
		}
	}
	if lk != nil {
		for _, s := range lk.Settings {
			key := strings.Split(s.Path, ".")
			for i := len(next.Layers) - 1; i >= 0 && next.Layers[i].Path != lookPath; i-- {
				if _, ok := lookupPath(next.Layers[i].Values, key); ok {
					p.Shadowed = append(p.Shadowed, LookShadow{Path: s.Path, File: displayPath(lc.Main, next.Layers[i].Path)})
					break
				}
			}
		}
	}
	return p, nil
}

// isLook reports whether the layer is the look file.
func (l ConfigLayer) isLook() bool {
	return l.Kind == LayerDropIn && filepath.Base(l.Path) == LookFileName
}

// Apply carries out the plan. The theme and glyph set go in first, so the
// config the watcher reloads names files that are there. A look applied over
// another removes the assets of the old one it does not reuse.
func (p *LookPlan) Apply() error {
	if p.Look == nil {
		return p.remove()
	}
	lk := p.Look
	if lk.Theme != nil {
		if _, err := theme.WriteLookAsset(false, lk.Theme.ID, lk.Theme.Data); err != nil {
			return err
		}
	}
	if lk.Glyphs != nil {
		if _, err := theme.WriteLookAsset(true, lk.Glyphs.ID, lk.Glyphs.Data); err != nil {
			return err
		}
	}
	data, err := lk.render(p.Source)
	if err != nil {
		return err
	}
	path := LookPath(p.Main)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := atomicfile.Write(path, data, 0o600, true); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	if c := p.Current; c != nil {
		if c.Theme != "" && (lk.Theme == nil || lk.Theme.ID != c.Theme) {
			_ = theme.RemoveLookAsset(false, c.Theme)
		}
		if c.Glyphs != "" && (lk.Glyphs == nil || lk.Glyphs.ID != c.Glyphs) {
			_ = theme.RemoveLookAsset(true, c.Glyphs)
		}
	}
	return nil
}

// remove deletes the look file first, so the config the watcher reloads no
// longer names the assets, then the assets.
func (p *LookPlan) remove() error {
	c := p.Current
	if c == nil {
		return nil
	}
	if err := os.Remove(c.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if c.Theme != "" {
		if err := theme.RemoveLookAsset(false, c.Theme); err != nil {
			return err
		}
	}
	if c.Glyphs != "" {
		if err := theme.RemoveLookAsset(true, c.Glyphs); err != nil {
			return err
		}
	}
	return nil
}
