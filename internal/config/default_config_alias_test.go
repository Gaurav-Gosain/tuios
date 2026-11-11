package config

import (
	"reflect"
	"testing"
)

// Each DefaultConfig call must return memory of its own. tuios ssh and
// tuios-web hold one config per connection, built from DefaultConfig, and
// SetOptionValue writes through a pointer field that is already set. A
// pointer, map or slice that two calls share is one setting for every
// connection: one client's settings page changed prefix_repeat_time for the
// other clients, and a reset to the default restored the changed value.

// TestSetOptionDoesNotReachAnotherDefaultConfig is the reported case: a set on
// one config must not change a second config or a later default.
func TestSetOptionDoesNotReachAnotherDefaultConfig(t *testing.T) {
	cases := []struct {
		path, value string
		read        func(*UserConfig) string
	}{
		{"appearance.prefix_repeat_time", "800", func(c *UserConfig) string {
			v, _ := GetOptionValue(c, "appearance.prefix_repeat_time")
			return v
		}},
		{"appearance.selection.flash", "false", func(c *UserConfig) string {
			v, _ := GetOptionValue(c, "appearance.selection.flash")
			return v
		}},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			before := tc.read(DefaultConfig())
			other := DefaultConfig()
			changed := DefaultConfig()
			if err := SetOptionValue(changed, tc.path, tc.value); err != nil {
				t.Fatal(err)
			}
			if got := tc.read(other); got != before {
				t.Errorf("another config reads %s = %s after a set on a different one, want %s", tc.path, got, before)
			}
			if got := tc.read(DefaultConfig()); got != before {
				t.Errorf("a new default config reads %s = %s, want %s", tc.path, got, before)
			}
		})
	}
}

// TestDefaultConfigSharesNoMemory walks two DefaultConfig results side by side
// and fails on any pointer, map or non-empty slice the two have in common, so
// the next field that points at a package variable fails here and not on a
// second client.
func TestDefaultConfigSharesNoMemory(t *testing.T) {
	var walk func(path string, a, b reflect.Value)
	walk = func(path string, a, b reflect.Value) {
		switch a.Kind() {
		case reflect.Pointer:
			if a.IsNil() || b.IsNil() {
				return
			}
			if a.Pointer() == b.Pointer() {
				t.Errorf("%s: two DefaultConfig calls share one pointer", path)
				return
			}
			walk(path, a.Elem(), b.Elem())
		case reflect.Map:
			if a.IsNil() || b.IsNil() {
				return
			}
			if a.Pointer() == b.Pointer() {
				t.Errorf("%s: two DefaultConfig calls share one map", path)
				return
			}
			for _, k := range a.MapKeys() {
				if bv := b.MapIndex(k); bv.IsValid() {
					walk(path+"["+k.String()+"]", a.MapIndex(k), bv)
				}
			}
		case reflect.Slice:
			if a.Cap() == 0 || b.Cap() == 0 {
				return
			}
			if a.Pointer() == b.Pointer() {
				t.Errorf("%s: two DefaultConfig calls share one slice", path)
				return
			}
			for i := range min(a.Len(), b.Len()) {
				walk(path+"[]", a.Index(i), b.Index(i))
			}
		case reflect.Struct:
			for i := range a.NumField() {
				if !a.Type().Field(i).IsExported() {
					continue
				}
				walk(path+"."+a.Type().Field(i).Name, a.Field(i), b.Field(i))
			}
		case reflect.Interface:
			if !a.IsNil() && !b.IsNil() {
				walk(path, a.Elem(), b.Elem())
			}
		}
	}
	walk("UserConfig", reflect.ValueOf(DefaultConfig()), reflect.ValueOf(DefaultConfig()))
}
