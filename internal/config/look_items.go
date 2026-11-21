package config

import (
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// LookList is the type of a look key whose value is a list of text.
const LookList = "list"

const lookMarkBytes = 32

type lookFileKey struct {
	Type        string
	Description string
	Default     string
	Accepted    []string
	Color       bool
	MaxBytes    int
	MaxItems    int
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
	"dock.tabs.active_fg":    tabColourKey("Text colour of the tab of the current workspace"),
	"dock.tabs.active_bg":    tabColourKey("Background colour of the tab of the current workspace"),
	"dock.tabs.active_bold":  {Type: OptionBool, Description: "Draw the flat tab of the current workspace in bold. A pill is always bold.", Default: "false"},
	"dock.tabs.active_underline": {
		Type: OptionBool, Description: "Underline the tab of the current workspace. Unset underlines a pill and not a flat tab.",
	},
	"dock.tabs.inactive_fg": tabColourKey("Text colour of the tabs of the other workspaces"),
	"dock.tabs.inactive_bg": tabColourKey("Background colour of the tabs of the other workspaces"),
	"dock.tabs.inactive_bold": {
		Type: OptionBool, Description: "Draw the tabs of the other workspaces in bold", Default: "false",
	},
	"dock.tabs.caps": {Type: OptionBool, Description: "Draw rounded ends on each flat tab, in the colour of the tab", Default: "false"},
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

func lookSectionItems() []string {
	return slices.DeleteFunc(SidebarLayoutNames(), func(s string) bool { return s == SidebarSectionCustom || s == SidebarSectionPane })
}

func lookItemKeys(path string) ([]string, bool) {
	if path == "appearance.sidebar.sections" {
		return lookSectionItems(), true
	}
	if k, ok := lookFileKeys[path]; ok && k.Type == LookList && k.Accepted != nil {
		return k.Accepted, true
	}
	return nil, false
}

func lookItemShown(path, item string) string { return lookKeyShown(path + ": " + item) }

func lookItems(path string, v any) (kept any, dropped []string, err error) {
	if path == "appearance.sidebar.sections" {
		s, ok := v.(string)
		if !ok {
			return nil, nil, errfmt.Errorf("%s must be text, such as \"sessions,files\"", path)
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
		return nil, errfmt.Errorf("%s must be a list of text, such as [\"a\", \"b\"]", path)
	}
	if len(raw) > spec.MaxItems {
		return nil, errfmt.Errorf("%s has %d items. A look may list at most %d", path, len(raw), spec.MaxItems)
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		s, ok := item.(string)
		if !ok {
			return nil, errfmt.Errorf("%s must be a list of text, such as [\"a\", \"b\"]", path)
		}
		if len(s) > spec.MaxBytes || hasControlChar(s) {
			return nil, errfmt.Errorf("each item of %s must be one line of at most %d bytes", path, spec.MaxBytes)
		}
		out = append(out, s)
	}
	return out, nil
}

func lookSections(s string) (any, []string, error) {
	if len(s) > maxLookString || hasControlChar(s) {
		return nil, nil, errfmt.Errorf("appearance.sidebar.sections must be one line of at most %d bytes", maxLookString)
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

func checkLookFileValue(path string, v any) error {
	spec := lookFileKeys[path]
	switch spec.Type {
	case OptionBool:
		if _, ok := v.(bool); !ok {
			return errfmt.Errorf("%s must be true or false", path)
		}
	case LookList:
		if _, ok := v.([]string); !ok {
			return errfmt.Errorf("%s must be a list of text, such as [\"a\", \"b\"]", path)
		}
	case OptionString:
		s, ok := v.(string)
		if !ok {
			return errfmt.Errorf("%s must be text", path)
		}
		if len(s) > spec.MaxBytes || hasControlChar(s) {
			return errfmt.Errorf("%s must be one line of at most %d bytes", path, spec.MaxBytes)
		}
		if spec.Color {
			if s != "" && !theme.IsColor(s) {
				return errfmt.Errorf("%s: %s. Use an empty value to unset it", path, ColourProblem(s))
			}
		} else if spec.Accepted != nil && !slices.Contains(spec.Accepted, s) {
			return errfmt.Errorf("%s: %q is not one of %s", path, s, strings.Join(spec.Accepted[1:], ", "))
		}
	}
	return nil
}

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
		return errfmt.Errorf("cannot set %s: no configuration is loaded", path)
	}
	if !IsLookKey(path) {
		return errfmt.Errorf("a look cannot set %s", path)
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
			return errfmt.Errorf("%s: %w", path, err)
		}
		v = b
	case LookList:
		var items []string
		if err := jsonx.Unmarshal([]byte(text), &items); err != nil {
			return errfmt.Errorf("%s must be a JSON list of text, such as [\"a\",\"b\"]", path)
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
		return errfmt.Errorf("unknown config option %q", path)
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
		if err := jsonx.Unmarshal([]byte(text), &items); err != nil {
			return errfmt.Errorf("%s must be a JSON list of text, such as [\"a\",\"b\"]", path)
		}
		v = items
	}
	kept, dropped, err := lookItems(path, v)
	if err != nil {
		return err
	}
	if len(dropped) > 0 {
		return errfmt.Errorf("%s names %s, which a look cannot set. A look names built-ins only", path, lookKeyShown(strings.Join(dropped, ", ")))
	}
	if kept == nil {
		return errfmt.Errorf("%s names no section", path)
	}
	return nil
}
