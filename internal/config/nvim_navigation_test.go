package config

import (
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestNvimNavigationConfig(t *testing.T) {
	for _, tc := range []struct {
		src  string
		want bool
	}{
		{"", false},
		{"[appearance]\nnvim_navigation = false\n", false},
		{"[appearance]\nnvim_navigation = true\n", true},
	} {
		var cfg UserConfig
		if err := toml.Unmarshal([]byte(tc.src), &cfg); err != nil {
			t.Fatalf("unmarshal %q: %v", tc.src, err)
		}
		s := DefaultSettings()
		ApplyAppearanceConfig(&cfg, &s)
		if s.NvimNavigation != tc.want {
			t.Errorf("%q gives %v, want %v", tc.src, s.NvimNavigation, tc.want)
		}
	}
	if DefaultConfig().Appearance.NvimNavigation == nil || *DefaultConfig().Appearance.NvimNavigation {
		t.Fatal("the default config does not disable nvim_navigation")
	}
	if _, ok := LookupOption("appearance.nvim_navigation"); !ok {
		t.Fatal("nvim_navigation is missing from the option registry")
	}
}
