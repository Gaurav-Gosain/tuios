package session

import (
	"reflect"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// TestEveryDaemonKeyReachesTheDaemon walks the [daemon] section by reflection
// and sets each key on its own, then checks the daemon's config changed for
// it. A key added to the section that this mapping ignores fails here, which
// is what three hand-written subsets never could say.
func TestEveryDaemonKeyReachesTheDaemon(t *testing.T) {
	prevLevel := GetDebugLevel()
	t.Cleanup(func() { SetDebugLevel(prevLevel) })

	baseline := func() *DaemonConfig {
		SetDebugLevel(DebugOff)
		return DaemonConfigFromUser(config.DefaultConfig())
	}
	base := baseline()

	typ := reflect.TypeFor[config.DaemonConfig]()
	for i := range typ.NumField() {
		field := typ.Field(i)
		t.Run(field.Name, func(t *testing.T) {
			uc := config.DefaultConfig()
			v := reflect.ValueOf(&uc.Daemon).Elem().Field(i)
			setNonZero(t, v)

			SetDebugLevel(DebugOff)
			got := DaemonConfigFromUser(uc)
			levelChanged := GetDebugLevel() != DebugOff
			if reflect.DeepEqual(got, base) && !levelChanged {
				t.Errorf("[daemon] %s set and the daemon's config did not change; the key is read by nothing", field.Tag.Get("toml"))
			}
		})
	}
}

// setNonZero gives a field a value that is not its zero.
func setNonZero(t *testing.T, v reflect.Value) {
	t.Helper()
	switch v.Kind() {
	case reflect.String:
		v.SetString("basic")
	case reflect.Int, reflect.Int64:
		v.SetInt(7)
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Slice:
		v.Set(reflect.Append(reflect.MakeSlice(v.Type(), 0, 1), reflect.Zero(v.Type().Elem())))
	case reflect.Ptr:
		elem := reflect.New(v.Type().Elem())
		if elem.Elem().Kind() == reflect.Bool {
			elem.Elem().SetBool(false)
		}
		v.Set(elem)
	default:
		t.Fatalf("no non-zero value for a %s; teach setNonZero the kind", v.Kind())
	}
}

// TestHostsAndHooksReachTheDaemon covers the two tables outside [daemon] the
// daemon owns.
func TestHostsAndHooksReachTheDaemon(t *testing.T) {
	uc := config.DefaultConfig()
	uc.Hosts = map[string]config.HostConfig{"box": {Addr: "box.example"}}
	uc.Hooks = map[string]any{"after_new_window": "true"}

	got := DaemonConfigFromUser(uc)
	if len(got.Hosts) != 1 || got.Hosts[0].Name != "box" || got.Hosts[0].Addr != "box.example" {
		t.Errorf("[hosts] reached the daemon as %+v", got.Hosts)
	}
	if got.Hooks == nil {
		t.Error("the hook table did not reach the daemon; a detached session runs none")
	}
}

// TestDaemonConfigFromNilAsksForNothing: no file, no settings, and the
// daemon's own defaults and TUIOS_* environment stand.
func TestDaemonConfigFromNilAsksForNothing(t *testing.T) {
	got := DaemonConfigFromUser(nil)
	if !reflect.DeepEqual(got, &DaemonConfig{}) {
		t.Errorf("a nil config produced %+v, want the zero value", got)
	}
}
