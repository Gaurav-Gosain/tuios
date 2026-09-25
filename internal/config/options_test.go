package config

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

// optionWalkSkips are the config paths the registry deliberately does not
// carry, each with the reason. A path that is skipped here has to be skipped
// for a reason a reader can check, so the walk below cannot quietly grow a
// hole.
var optionWalkSkips = map[string]string{
	"keybindings":                  "maps of action to keys, not scalar paths",
	"hooks":                        "a free-form map of event to command",
	"daemon.agent_binaries":        "a list, which a value arriving as one string cannot spell",
	"daemon.respond_from_shell":    "a grant to act as the person, which set-option, a verb any pane can call, must not be able to switch",
	"notifications.agent.sounds":   "file paths, which no accepted set or range can check",
	"dock.left":                    "an ordered list of component names, not a scalar path",
	"dock.center":                  "an ordered list of component names, not a scalar path",
	"dock.right":                   "an ordered list of component names, not a scalar path",
	"dock.custom":                  "a free-form map of component name to its command and refresh",
	"hosts":                        "a map of host name to its address, which no single settable path can spell",
	"tailscale":                    "file-plane config for what the hosts table suggests, read from the file by the two callers that use it, like [hosts] above",
	"appearance.sidebar.agent_row": "a table of tokens, each with a look and an ordered rule list, which no single settable path can spell",
	"agents":                       "file-plane config the daemon reads from the file, like [hosts]: which harnesses hold their prompts for the Inbox is not for a pane to change over the control protocol",
}

// TestOptionRegistryCoversEveryScalarField is the guard that keeps the
// hand-written table and the config structs in step: a field added to
// UserConfig without an entry fails here rather than being silently unreachable
// from the control protocol.
func TestOptionRegistryCoversEveryScalarField(t *testing.T) {
	fields := walkScalarPaths(t, reflect.TypeOf(UserConfig{}), "")

	for _, path := range fields {
		if _, ok := LookupOption(path); !ok {
			t.Errorf("config field %q has no registry entry", path)
		}
	}
	for _, opt := range Options() {
		if !slices.Contains(fields, opt.Path) {
			t.Errorf("registry entry %q names no field on UserConfig", opt.Path)
		}
	}

	paths := OptionPaths()
	if !slices.IsSorted(paths) {
		t.Error("OptionPaths is not sorted")
	}
	if len(slices.Compact(slices.Clone(paths))) != len(paths) {
		t.Error("OptionPaths has a duplicate")
	}
	if len(paths) != len(optionSpecs) {
		t.Errorf("OptionPaths has %d paths for %d specs", len(paths), len(optionSpecs))
	}
}

// walkScalarPaths collects the dotted toml path of every scalar field reachable
// from t, recursing into nested tables and honouring optionWalkSkips.
func walkScalarPaths(t *testing.T, structType reflect.Type, prefix string) []string {
	t.Helper()

	var paths []string
	for i := range structType.NumField() {
		field := structType.Field(i)
		name := tomlFieldName(field)
		if name == "" {
			continue
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		if _, skip := optionWalkSkips[path]; skip {
			continue
		}

		fieldType := field.Type
		if fieldType.Kind() == reflect.Pointer {
			fieldType = fieldType.Elem()
		}
		switch fieldType.Kind() {
		case reflect.Bool, reflect.Int, reflect.String:
			paths = append(paths, path)
		case reflect.Struct:
			paths = append(paths, walkScalarPaths(t, fieldType, path)...)
		default:
			t.Errorf("field %q is a %s that is neither scalar nor skipped", path, fieldType.Kind())
		}
	}
	return paths
}

func TestSetOptionValueRejectsBadInput(t *testing.T) {
	cases := []struct{ name, path, value string }{
		{"unknown path", "appearance.no_such_key", "1"},
		{"outside the accepted set", "appearance.border_style", "wobbly"},
		{"outside auto-enter set", "appearance.auto_enter_terminal_on_focus", "sometimes"},
		{"non-numeric int", "appearance.scroll_lines", "several"},
		{"out-of-range int", "appearance.scroll_lines", "9999"},
		{"unparseable bool", "appearance.hide_window_buttons", "maybe"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			err := SetOptionValue(cfg, tc.path, tc.value)
			if err == nil {
				t.Fatalf("set %s=%q was accepted", tc.path, tc.value)
			}
			if !strings.Contains(err.Error(), tc.path) {
				t.Errorf("error %q does not name the path %q", err, tc.path)
			}
		})
	}

	if _, ok := GetOptionValue(DefaultConfig(), "appearance.no_such_key"); ok {
		t.Error("GetOptionValue reported an unknown path as found")
	}
}
