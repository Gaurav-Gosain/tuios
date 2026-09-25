package cliflags

import (
	"reflect"
	"testing"

	"github.com/spf13/pflag"
)

// setEveryFlag parses a value for every flag Register adds, each one different
// from its default.
func setEveryFlag(t *testing.T, i *Interface) {
	t.Helper()
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	i.Register(fs)
	var args []string
	fs.VisitAll(func(f *pflag.Flag) {
		switch f.Value.Type() {
		case "bool":
			args = append(args, "--"+f.Name)
		case "int":
			args = append(args, "--"+f.Name+"=200")
		default:
			args = append(args, "--"+f.Name+"=x")
		}
	})
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}
}

// TestEveryFlagReachesItsField catches a flag registered against nothing, or a
// field of Interface no flag sets.
func TestEveryFlagReachesItsField(t *testing.T) {
	var i Interface
	setEveryFlag(t, &i)
	v := reflect.ValueOf(i)
	for n := 0; n < v.NumField(); n++ {
		if v.Field(n).IsZero() {
			t.Errorf("no flag sets Interface.%s", v.Type().Field(n).Name)
		}
	}
}

// TestEveryOverrideIsMapped catches an override field Overrides forgets, which
// is how a flag ends up registered and then ignored.
func TestEveryOverrideIsMapped(t *testing.T) {
	var i Interface
	setEveryFlag(t, &i)
	o := reflect.ValueOf(i.Overrides())
	for n := 0; n < o.NumField(); n++ {
		if o.Field(n).IsZero() {
			t.Errorf("Overrides leaves config.Overrides.%s unset", o.Type().Field(n).Name)
		}
	}
}
