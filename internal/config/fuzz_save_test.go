package config

import (
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
)

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
		"[appearance]\nmax_fps = 0\n",
		"[appearance]\nmax_fps = \"AUTO\"\n",
		"[appearance]\nmax_fps = \"0144\"\n",
		"[appearance]\nmax_fps = \"fa\\\"st\\u0000/\"\n",
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
