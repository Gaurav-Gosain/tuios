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
	"keybindings":                "maps of action to keys, not scalar paths",
	"hooks":                      "a free-form map of event to command",
	"daemon.agent_binaries":      "a list, which a value arriving as one string cannot spell",
	"notifications.agent.sounds": "file paths, which no accepted set or range can check",
	"dock.left":                  "an ordered list of component names, not a scalar path",
	"dock.center":                "an ordered list of component names, not a scalar path",
	"dock.right":                 "an ordered list of component names, not a scalar path",
	"dock.custom":                "a free-form map of component name to its command and refresh",
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

func TestSetOptionValueRoundTrips(t *testing.T) {
	cases := []struct {
		path, set, want string
	}{
		{"appearance.hide_window_buttons", "on", "true"},   // bool
		{"appearance.sidebar.enabled", "false", "false"},   // *bool, and an explicit false
		{"appearance.scrollback_lines", "500", "500"},      // int
		{"notifications.agent.settle_seconds", "0", "0"},   // *int, and an explicit zero
		{"appearance.border_style", "double", "double"},    // string
		{"appearance.word_characters", "", ""},             // *string, and an explicit empty
		{"appearance.sidebar.position", "right", "right"},  // nested table
		{"notifications.agent.states.idle", "yes", "true"}, // twice-nested table
	}

	for _, tc := range cases {
		cfg := DefaultConfig()
		if err := SetOptionValue(cfg, tc.path, tc.set); err != nil {
			t.Fatalf("set %s=%q: %v", tc.path, tc.set, err)
		}
		got, ok := GetOptionValue(cfg, tc.path)
		if !ok {
			t.Fatalf("get %s: not found after setting it", tc.path)
		}
		if got != tc.want {
			t.Errorf("set %s=%q, read back %q, want %q", tc.path, tc.set, got, tc.want)
		}
	}
}

func TestSetOptionValueRejectsBadInput(t *testing.T) {
	cases := []struct{ name, path, value string }{
		{"unknown path", "appearance.no_such_key", "1"},
		{"outside the accepted set", "appearance.border_style", "wobbly"},
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

// TestOptionDefaultsMatchDefaultConfig pins each entry's Default to what the
// app actually starts with, so a mistyped default is a failure rather than a
// wrong answer to a caller asking what a setting is.
func TestOptionDefaultsMatchDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	for _, opt := range Options() {
		got, ok := GetOptionValue(cfg, opt.Path)
		if !ok {
			t.Errorf("%s: not readable from a default config", opt.Path)
			continue
		}
		if got != opt.Default {
			t.Errorf("%s: default config holds %q, registry says %q", opt.Path, got, opt.Default)
		}
	}
}

// TestOptionSpecsAreWellFormed checks the parts of an entry that only a human
// writes, since nothing else reads them closely enough to notice a blank one.
func TestOptionSpecsAreWellFormed(t *testing.T) {
	sections := []string{
		"appearance", "sidebar", "dock", "scrollbar",
		"startup", "daemon", "notifications", "tape", "debug",
	}
	for _, opt := range optionSpecs {
		if opt.Description == "" {
			t.Errorf("%s: no description", opt.Path)
		}
		if !slices.Contains(sections, opt.Section) {
			t.Errorf("%s: section %q is not one of the display groups", opt.Path, opt.Section)
		}
		switch opt.Type {
		case OptionBool, OptionInt, OptionString:
		default:
			t.Errorf("%s: type %q is not bool, int or string", opt.Path, opt.Type)
		}
		if opt.Type != OptionInt && (opt.Min != 0 || opt.Max != 0) {
			t.Errorf("%s: a %s option carries a range", opt.Path, opt.Type)
		}
		if len(opt.Accepted) > 0 && !slices.Contains(opt.Accepted, opt.Default) && opt.Default != "" {
			t.Errorf("%s: default %q is outside its own accepted set", opt.Path, opt.Default)
		}
	}
}
