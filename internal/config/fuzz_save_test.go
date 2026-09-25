package config

import (
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

// Fuzzing the save half of the config: tuios writes the config file itself
// (set-option with persist, the settings panel), so what it writes has to load
// back as the config it wrote.
//
// The ways this could fail, written down before the target:
//
//  1. A config that loads cannot be rendered: a value the TOML encoder
//     refuses.
//  2. The rendered file does not parse, so the next start falls back to
//     defaults and every setting the user had is gone.
//  3. It parses to a different config: a field the encoder drops or spells
//     differently, a value the fill stage rewrites on the second load, a key
//     binding whose spelling changes. Saving one setting then quietly changes
//     another.
//  4. Rendering depends on map order, so saving an unchanged config rewrites
//     the file.

var configCmp = []cmp.Option{
	cmpopts.EquateEmpty(),
	cmpopts.EquateNaNs(),
	cmp.Exporter(func(reflect.Type) bool { return true }),
}

func FuzzConfigSaveRoundTrip(f *testing.F) {
	for _, s := range []string{
		"",
		"[appearance]\nborder_style = \"rounded\"\nscrollback_lines = 5000\n",
		"[keybindings]\nleader_key = \"ctrl+a\"\n[keybindings.terminal_mode]\nquit = [\"ctrl+q\"]\n",
		"[keybindings.workspace]\nnext = [\"\", \"alt+n\"]\n",
		"[dock]\nleft = [\"mode\"]\ncenter = []\nright = [\"clock\"]\n[dock.custom.branch]\ncommand = \"git branch\"\nrefresh = \"30s\"\n",
		"[daemon]\nlog_level = \"debug\"\n",
		"[appearance]\ntitle_format = \"\\u0000\\\"'''\"\n",
		"[hooks]\n",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, src string) {
		if len(src) > 1<<14 {
			return
		}
		cfg, err := ParseUserConfig([]byte(src))
		if err != nil {
			return
		}
		out, err := renderConfigFile(cfg, "/tmp/tuios-fuzz/config.toml")
		if err != nil {
			t.Fatalf("a config that loaded cannot be saved: %v\nsource:\n%s", err, src)
		}
		again, err := ParseUserConfig(out)
		if err != nil {
			t.Fatalf("the saved config does not load: %v\nsaved:\n%s", err, clip(string(out)))
		}
		if diff := cmp.Diff(cfg, again, configCmp...); diff != "" {
			t.Fatalf("the saved config loads as a different config (-loaded +reloaded):\n%s\nsource:\n%s",
				clip(diff), clip(src))
		}
		out2, err := renderConfigFile(again, "/tmp/tuios-fuzz/config.toml")
		if err != nil {
			t.Fatalf("the reloaded config cannot be saved: %v", err)
		}
		if string(out2) != string(out) {
			t.Fatalf("saving the same config twice wrote different files")
		}
	})
}

func clip(s string) string {
	if len(s) > 3000 {
		return s[:3000] + "\n... " + strings.Repeat(".", 3)
	}
	return s
}
