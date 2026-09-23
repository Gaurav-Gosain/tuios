package config

import "testing"

// TestValidateKeyModifiersOnEachPlatform pins which modifiers ValidateKey
// accepts, and the reason it gives for the rest, on macOS and elsewhere.
func TestValidateKeyModifiersOnEachPlatform(t *testing.T) {
	cases := []struct {
		key    string
		mac    string // "" when valid on macOS, else the reason
		others string // "" when valid elsewhere, else the reason
	}{
		{"ctrl+a", "", ""},
		{"alt+a", "", ""},
		{"shift+tab", "", ""},
		{"super+v", "", ""},
		{"shift+super+v", "", ""},
		{"ctrl+alt+shift+super+x", "", ""},
		{"opt+a", "", "opt/option keys are only valid on macOS, use alt+ instead"},
		{"option+a", "", "opt/option keys are only valid on macOS, use alt+ instead"},
		{"meta+a", "invalid modifier: meta", "invalid modifier: meta"},
		{"hyper+a", "invalid modifier: hyper", "invalid modifier: hyper"},
		{"ctrl+ctrl+a", "duplicate modifier: ctrl", "duplicate modifier: ctrl"},
		{"ctrl+", "key combination incomplete (ends with +)", "key combination incomplete (ends with +)"},
		{"ctrl+enter", "", ""},
		{"ctrl+capslock", "", ""},
		{"ctrl+f12", "", ""},
		{"ctrl+nosuchkey", "unknown special key: nosuchkey", "unknown special key: nosuchkey"},
		{"nosuchkey", "unknown special key: nosuchkey", "unknown special key: nosuchkey"},
		{"leftalt", "", ""},
	}
	for _, tc := range cases {
		for _, platform := range []struct {
			name  string
			isMac bool
			want  string
		}{{"macOS", true, tc.mac}, {"other", false, tc.others}} {
			kn := &KeyNormalizer{isMacOS: platform.isMac}
			ok, reason := kn.ValidateKey(tc.key)
			if ok != (platform.want == "") || reason != platform.want {
				t.Errorf("%s: ValidateKey(%q) = %v, %q; want %v, %q",
					platform.name, tc.key, ok, reason, platform.want == "", platform.want)
			}
		}
	}
}

// TestValidateKeyDoesNotAllocateForAPlainBinding holds the lookup tables out
// of the call. They were map literals built on every call, which was about
// half of what loading a large config allocated.
func TestValidateKeyDoesNotAllocateForAPlainBinding(t *testing.T) {
	kn := &KeyNormalizer{isMacOS: true}
	for _, key := range []string{"n", "enter", "pgdown"} {
		allocs := testing.AllocsPerRun(100, func() {
			if ok, reason := kn.ValidateKey(key); !ok {
				t.Fatalf("ValidateKey(%q) rejected: %s", key, reason)
			}
		})
		if allocs > 2 {
			t.Errorf("ValidateKey(%q) allocates %v times", key, allocs)
		}
	}
}

// BenchmarkValidateConfigDefault validates the default config, which checks
// every default binding through ValidateKey.
func BenchmarkValidateConfigDefault(b *testing.B) {
	cfg := DefaultConfig()
	b.ReportAllocs()
	for b.Loop() {
		ValidateConfig(cfg)
	}
}
