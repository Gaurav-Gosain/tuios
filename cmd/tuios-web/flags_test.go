package main

import (
	"testing"

	"github.com/spf13/pflag"

	"github.com/Gaurav-Gosain/tuios/internal/cliflags"
)

// TestWebTakesEveryInterfaceFlag holds tuios-web to the interface flags `tuios`
// and `tuios ssh` take. It used to register nine of the eighteen overrides, so
// --hide-scrollbar, --shared-borders, --confirm-quit and the rest could only be
// set from the config file for a browser.
func TestWebTakesEveryInterfaceFlag(t *testing.T) {
	want := pflag.NewFlagSet("want", pflag.ContinueOnError)
	var scratch cliflags.Interface
	scratch.Register(want)

	root := newRootCmd()
	want.VisitAll(func(f *pflag.Flag) {
		if root.Flags().Lookup(f.Name) == nil {
			t.Errorf("tuios-web does not take --%s", f.Name)
		}
	})
}

// TestWebFlagsReachTheOverrides checks the flags are not only registered but
// read: a parsed flag has to come out of webAppearanceOverrides.
func TestWebFlagsReachTheOverrides(t *testing.T) {
	saved := interfaceFlags
	t.Cleanup(func() { interfaceFlags = saved })

	root := newRootCmd()
	if err := root.Flags().Parse([]string{"--shared-borders", "--zoom-max-width=120", "--window-title-position=top"}); err != nil {
		t.Fatal(err)
	}
	o := webAppearanceOverrides()
	if !o.SharedBorders || o.ZoomMaxWidth != 120 || o.WindowTitlePosition != "top" {
		t.Errorf("the parsed flags did not reach the overrides: %+v", o)
	}
}
