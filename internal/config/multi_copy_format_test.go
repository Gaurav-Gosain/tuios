package config

import (
	"testing"

	"github.com/pelletier/go-toml/v2"
)

// appearance.selection.multi_format picks the format multi copy mode starts
// in. A value it does not know leaves plain, the default.
func TestMultiCopyFormatConfig(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"", MultiCopyFormatPlain},
		{"[appearance.selection]\nmulti_format = \"markdown\"\n", MultiCopyFormatMarkdown},
		{"[appearance.selection]\nmulti_format = \"json\"\n", MultiCopyFormatJSON},
		{"[appearance.selection]\nmulti_format = \"yaml\"\n", MultiCopyFormatPlain},
	} {
		var cfg UserConfig
		if err := toml.Unmarshal([]byte(tc.src), &cfg); err != nil {
			t.Fatalf("unmarshal %q: %v", tc.src, err)
		}
		s := DefaultSettings()
		ApplyAppearanceConfig(&cfg, &s)
		if s.MultiCopyFormat != tc.want {
			t.Errorf("%q gives %q, want %q", tc.src, s.MultiCopyFormat, tc.want)
		}
	}
	if DefaultConfig().Appearance.Selection.MultiFormat != MultiCopyFormatPlain {
		t.Error("the default config does not write multi_format = plain")
	}
	opt, ok := LookupOption("appearance.selection.multi_format")
	if !ok || len(opt.Accepted) != len(MultiCopyFormats) {
		t.Errorf("the option registry does not list multi_format with its formats: %+v", opt)
	}
}
