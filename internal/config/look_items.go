package config

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// Look keys outside the settings registry, and the keys checked item by item.
//
// The dock's three lists and its [dock.tabs] table live in the config file
// only, so the registry does not carry them. A look may still set them: the
// lists say which built-in cells the bar draws and on which side, and the tabs
// table colours the workspace strip. That is how the bar looks, and nothing
// else.
//
// Two kinds of list can name something that runs a command. "custom/NAME" in a
// dock list draws the [dock.custom.NAME] cell, whose command runs through
// sh -c, and "custom" in appearance.sidebar.sections draws the rail section
// whose command appearance.sidebar.custom sets. A look cannot set either
// command. The person's own config may hold one that no list names yet,
// though, and a look that named it would start it. So each item is checked on
// its own: an item that names a built-in stays, any other item is left out and
// named, and the rest of the list applies.

// LookList is the type of a look key whose value is a list of text.
const LookList = "list"

// lookMarkBytes caps a separator, a marker and one workspace glyph.
const lookMarkBytes = 32

// lookFileKey is a look key the registry does not carry.
type lookFileKey struct {
	Type        string // OptionBool, OptionString or LookList
	Description string
	Default     string
	// Accepted is a string's accepted set, or the names a list item may be.
	Accepted []string
	Color    bool
	// MaxBytes caps a text value, or one list item.
	MaxBytes int
	MaxItems int
}

func dockListKey(side string, def []string) lookFileKey {
	return lookFileKey{
		Type:        LookList,
		Description: "The built-in cells " + side + " of the dock, in order. An empty list draws nothing there.",
		Default:     lookValueText(def),
		Accepted:    DockBuiltinComponents(),
		MaxBytes:    lookMarkBytes,
		MaxItems:    2 * len(dockBuiltins),
	}
}

func tabColourKey(desc string) lookFileKey {
	return lookFileKey{Type: OptionString, Description: desc, Color: true, MaxBytes: maxLookString}
}

func tabMarkKey(desc string) lookFileKey {
	return lookFileKey{Type: OptionString, Description: desc, MaxBytes: lookMarkBytes}
}

// lookFileKeys are the look keys the registry does not carry.
var lookFileKeys = map[string]lookFileKey{
	"dock.left":   dockListKey("on the left end", defaultDockLeft),
	"dock.center": dockListKey("in the middle", defaultDockCenter),
	"dock.right":  dockListKey("on the right end", defaultDockRight),

	"dock.tabs.style": {
		Type: OptionString, Description: "How the workspace strip draws its tabs: pill, or flat for coloured runs of text",
		Accepted: []string{"", DockTabStylePill, DockTabStyleFlat}, MaxBytes: lookMarkBytes,
	},
	"dock.tabs.separator":    tabMarkKey("Text between two flat tabs. Empty is one blank column."),
	"dock.tabs.separator_fg": tabColourKey("Colour of the text between two flat tabs"),
	"dock.tabs.active_fg":    tabColourKey("Text colour of the flat tab of the current workspace"),
	"dock.tabs.active_bg":    tabColourKey("Background colour of the flat tab of the current workspace"),
	"dock.tabs.active_bold":  {Type: OptionBool, Description: "Draw the flat tab of the current workspace in bold", Default: "false"},
	"dock.tabs.inactive_fg":  tabColourKey("Text colour of the flat tabs of the other workspaces"),
	"dock.tabs.inactive_bg":  tabColourKey("Background colour of the flat tabs of the other workspaces"),
	"dock.tabs.inactive_bold": {
		Type: OptionBool, Description: "Draw the flat tabs of the other workspaces in bold", Default: "false",
	},
	"dock.tabs.number_glyphs": {
		Type:        LookList,
		Description: "What {icon} in appearance.dock_workspace_tab_format shows for each workspace, the first item for workspace 1. A workspace past the end shows its number.",
		Default:     "[]", MaxBytes: lookMarkBytes, MaxItems: 9,
	},
	"dock.tabs.zoom_marker":     tabMarkKey("What {zoom} shows on the tab of the current workspace while a pane in it is zoomed"),
	"dock.tabs.show_add":        {Type: OptionBool, Description: "Keep the + tab in the flat style", Default: "false"},
	"dock.tabs.attention_fg":    tabColourKey("Text colour of the tab of a workspace with a pane that waits for you"),
	"dock.tabs.bell_marker":     tabMarkKey("Text after the label of a workspace whose pane rang the bell. Empty shows none."),
	"dock.tabs.activity_marker": tabMarkKey("Text after the label of a workspace whose pane printed since you looked. Empty shows none."),
}

// IsLookFileKey reports whether path is a look key the registry does not
// carry: a dock list or a [dock.tabs] key. SetLookValue sets one.
func IsLookFileKey(path string) bool {
	_, ok := lookFileKeys[path]
	return ok
}

// lookSectionItems are the rail sections a look may list: every built-in
// section but custom and pane, and the spacer. Both run a command, and a look
// carries no commands.
func lookSectionItems() []string {
	return slices.DeleteFunc(SidebarLayoutNames(), func(s string) bool { return s == SidebarSectionCustom || s == SidebarSectionPane })
}

// lookItemKeys are the look keys checked item by item, with the items each
// accepts. dock.tabs.number_glyphs is a list too, but every item is only text.
func lookItemKeys(path string) ([]string, bool) {
	if path == "appearance.sidebar.sections" {
		return lookSectionItems(), true
	}
	if k, ok := lookFileKeys[path]; ok && k.Type == LookList && k.Accepted != nil {
		return k.Accepted, true
	}
	return nil, false
}

// lookItemShown is how a left-out item is named: the key, then the item.
func lookItemShown(path, item string) string { return lookKeyShown(path + ": " + item) }

// lookItems checks a value item by item. It returns the value to keep and the
// items it leaves out. A nil kept value means no item is left, so the key is
// left out. A value of the wrong type, or an item that is not one line of
// short text, is an error.
func lookItems(path string, v any) (kept any, dropped []string, err error) {
	if path == "appearance.sidebar.sections" {
		s, ok := v.(string)
		if !ok {
			return nil, nil, fmt.Errorf("%s must be text, such as \"sessions,files\"", path)
		}
		return lookSections(s)
	}
	spec, ok := lookFileKeys[path]
	if !ok || spec.Type != LookList {
		return v, nil, nil
	}
	items, err := lookTextList(path, v, spec)
	if err != nil {
		return nil, nil, err
	}
	if spec.Accepted == nil {
		return items, nil, nil
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		if slices.Contains(spec.Accepted, strings.TrimSpace(it)) {
			out = append(out, strings.TrimSpace(it))
		} else {
			dropped = append(dropped, it)
		}
	}
	return out, dropped, nil
}

// lookTextList reads a TOML array of one-line strings.
func lookTextList(path string, v any, spec lookFileKey) ([]string, error) {
	var raw []any
	switch v := v.(type) {
	case []any:
		raw = v
	case []string:
		for _, s := range v {
			raw = append(raw, s)
		}
	default:
		return nil, fmt.Errorf("%s must be a list of text, such as [\"a\", \"b\"]", path)
	}
	if len(raw) > spec.MaxItems {
		return nil, fmt.Errorf("%s has %d items. A look may list at most %d", path, len(raw), spec.MaxItems)
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("%s must be a list of text, such as [\"a\", \"b\"]", path)
		}
		if len(s) > spec.MaxBytes || hasControlChar(s) {
			return nil, fmt.Errorf("each item of %s must be one line of at most %d bytes", path, spec.MaxBytes)
		}
		out = append(out, s)
	}
	return out, nil
}

// lookSections keeps the entries of a rail layout that name a built-in
// section or the spacer, each with its share. A layout with no section left
// is left out whole, since the rail would fall back to its default.
func lookSections(s string) (any, []string, error) {
	if len(s) > maxLookString || hasControlChar(s) {
		return nil, nil, fmt.Errorf("appearance.sidebar.sections must be one line of at most %d bytes", maxLookString)
	}
	allowed := lookSectionItems()
	var keep, dropped []string
	sections := 0
	for field := range strings.SplitSeq(s, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		name, _, _ := strings.Cut(field, ":")
		name = strings.ToLower(strings.TrimSpace(name))
		if !slices.Contains(allowed, name) {
			dropped = append(dropped, field)
			continue
		}
		if name != SidebarSectionSpacer {
			sections++
		}
		keep = append(keep, field)
	}
	if sections == 0 {
		return nil, dropped, nil
	}
	return strings.Join(keep, ","), dropped, nil
}

// checkLookFileValue checks the value of a key the registry does not carry,
// as set-config checks a registry option.
func checkLookFileValue(path string, v any) error {
	spec := lookFileKeys[path]
	switch spec.Type {
	case OptionBool:
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("%s must be true or false", path)
		}
	case LookList:
		if _, ok := v.([]string); !ok {
			return fmt.Errorf("%s must be a list of text, such as [\"a\", \"b\"]", path)
		}
	case OptionString:
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("%s must be text", path)
		}
		if len(s) > spec.MaxBytes || hasControlChar(s) {
			return fmt.Errorf("%s must be one line of at most %d bytes", path, spec.MaxBytes)
		}
		if spec.Color {
			if s != "" && !theme.IsColor(s) {
				return fmt.Errorf("%s: %s. Use an empty value to unset it", path, ColourProblem(s))
			}
		} else if spec.Accepted != nil && !slices.Contains(spec.Accepted, s) {
			return fmt.Errorf("%s: %q is not one of %s", path, s, strings.Join(spec.Accepted[1:], ", "))
		}
	}
	return nil
}

// lookFileValueText reads a key the registry does not carry from cfg, as the
// plan prints it.
func lookFileValueText(cfg *UserConfig, path string) string {
	field, ok := resolveOptionField(cfg, path)
	if !ok {
		return ""
	}
	if field.Kind() == reflect.Pointer {
		if field.IsNil() {
			return lookFileKeys[path].Default
		}
		field = field.Elem()
	}
	switch field.Kind() {
	case reflect.Bool:
		return strconv.FormatBool(field.Bool())
	case reflect.String:
		return field.String()
	case reflect.Slice:
		items, _ := field.Interface().([]string)
		return lookValueText(items)
	}
	return ""
}

// SetLookValue sets a look key on cfg from text, for a page of the browser
// build (internal/learn). A list is a JSON array of strings, such as
// ["mode","workspaces"]. It takes only what a look file may hold: an item a
// look leaves out is an error here, so the page never shows a bar or a rail
// that the look file would not give.
func SetLookValue(cfg *UserConfig, path, text string) error {
	if cfg == nil {
		return fmt.Errorf("cannot set %s: no configuration is loaded", path)
	}
	if !IsLookKey(path) {
		return fmt.Errorf("a look cannot set %s", path)
	}
	if err := CheckLookItems(path, text); err != nil {
		return err
	}
	spec, file := lookFileKeys[path]
	if !file {
		return SetOptionValue(cfg, path, text)
	}
	var v any = text
	switch spec.Type {
	case OptionBool:
		b, err := parseOptionBool(text)
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		v = b
	case LookList:
		var items []string
		if err := json.Unmarshal([]byte(text), &items); err != nil {
			return fmt.Errorf("%s must be a JSON list of text, such as [\"a\",\"b\"]", path)
		}
		kept, _, err := lookItems(path, items)
		if err != nil {
			return err
		}
		v = kept
	}
	if err := checkLookFileValue(path, v); err != nil {
		return err
	}
	field, ok := resolveOptionField(cfg, path)
	if !ok {
		return fmt.Errorf("unknown config option %q", path)
	}
	target := optionTarget(field)
	switch v := v.(type) {
	case bool:
		target.SetBool(v)
	case string:
		target.SetString(v)
	case []string:
		target.Set(reflect.ValueOf(slices.Clone(v)))
	}
	return nil
}

// CheckLookItems refuses a value of a key checked item by item when it holds
// an item a look leaves out. text is the value as SetLookValue takes it.
func CheckLookItems(path, text string) error {
	if _, ok := lookItemKeys(path); !ok {
		return nil
	}
	var v any = text
	if path != "appearance.sidebar.sections" {
		var items []string
		if err := json.Unmarshal([]byte(text), &items); err != nil {
			return fmt.Errorf("%s must be a JSON list of text, such as [\"a\",\"b\"]", path)
		}
		v = items
	}
	kept, dropped, err := lookItems(path, v)
	if err != nil {
		return err
	}
	if len(dropped) > 0 {
		return fmt.Errorf("%s names %s, which a look cannot set. A look names built-ins only", path, lookKeyShown(strings.Join(dropped, ", ")))
	}
	if kept == nil {
		return fmt.Errorf("%s names no section", path)
	}
	return nil
}
