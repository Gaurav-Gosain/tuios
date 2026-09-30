package config

import (
	"strings"
	"testing"
)

func TestScratchDefaults(t *testing.T) {
	s := DefaultConfig().Scratch
	if s.Session != "" || s.WidthSpec() != "80%" || s.HeightSpec() != "80%" {
		t.Fatalf("defaults = %+v", s)
	}
	if got := DefaultConfig().Keybindings.PrefixMode["toggle_scratch"]; len(got) != 1 || got[0] != "g" {
		t.Fatalf("toggle_scratch default key = %v, want [g]", got)
	}
}

func TestScratchTableParses(t *testing.T) {
	cfg, err := ParseUserConfig([]byte("[scratch]\nwidth = \"100\"\nheight = \"50%\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	s := cfg.Scratch
	if s.WidthSpec() != "100" || s.HeightSpec() != "50%" {
		t.Fatalf("parsed = %+v", s)
	}
}

// A config written for the first design still has the session key. It loads,
// the sizes still apply, and validation says the key is no longer used.
func TestScratchOldSessionKeyLoadsAndWarns(t *testing.T) {
	cfg, err := ParseUserConfig([]byte("[scratch]\nsession = \"notes\"\nwidth = \"70%\"\n"))
	if err != nil {
		t.Fatalf("a config with the old key did not load: %v", err)
	}
	if cfg.Scratch.WidthSpec() != "70%" || cfg.Scratch.HeightSpec() != "80%" {
		t.Fatalf("sizes = %+v", cfg.Scratch)
	}
	res := ValidateConfig(cfg)
	if res.HasErrors() {
		t.Fatalf("the old key is an error, want a warning: %+v", res.Errors)
	}
	found := false
	for _, w := range res.Warnings {
		if w.Field == "scratch" && w.Key == "session" && strings.Contains(w.Message, "no longer used") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no warning about the old session key: %+v", res.Warnings)
	}
	// The key is gone from the options, so set-config refuses it.
	if err := SetOptionValue(cfg, "scratch.session", "notes"); err == nil {
		t.Fatal("set-config accepted scratch.session")
	}
}

// A file without the table, or with only part of it, gets the defaults for
// what it leaves out.
func TestScratchTableFillsWhatIsMissing(t *testing.T) {
	cfg, err := ParseUserConfig([]byte("[scratch]\nwidth = \"70%\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	s := cfg.Scratch
	if s.Width != "70%" || s.Height != "80%" {
		t.Fatalf("filled = %+v", s)
	}
}

// A size that does not parse falls back to the default, as a popup does.
func TestScratchBadSizeFallsBack(t *testing.T) {
	s := ScratchConfig{Width: "wide", Height: "0"}
	if s.WidthSpec() != ScratchDefaultWidth || s.HeightSpec() != ScratchDefaultHeight {
		t.Fatalf("specs = %q %q", s.WidthSpec(), s.HeightSpec())
	}
}

func TestScratchValidation(t *testing.T) {
	cases := []struct {
		name string
		s    ScratchConfig
		want string // "" means no warning
	}{
		{"defaults", defaultScratchConfig(), ""},
		{"cells", ScratchConfig{Width: "100", Height: "30"}, ""},
		{"small cells", ScratchConfig{Width: "10", Height: "5"}, ""},
		{"percent", ScratchConfig{Width: "5%", Height: "5%"}, ""},
		{"not a size", ScratchConfig{Width: "wide"}, "is not a number"},
		{"too many percent", ScratchConfig{Height: "120%"}, "more than the whole region"},
		{"old session key", ScratchConfig{Session: "notes"}, "no longer used"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Scratch = tc.s
			res := &ValidationResult{}
			validateScratch(cfg, res)
			var got []string
			for _, w := range res.Warnings {
				got = append(got, w.Message)
			}
			joined := strings.Join(got, " | ")
			if tc.want == "" {
				if len(got) != 0 {
					t.Fatalf("warnings = %s, want none", joined)
				}
				return
			}
			if !strings.Contains(joined, tc.want) {
				t.Fatalf("warnings = %q, want one with %q", joined, tc.want)
			}
		})
	}
}

// set-option checks a scratch size the way the popup verb does, and still
// takes the empty string as "the default".
func TestScratchSizeOptionIsChecked(t *testing.T) {
	cfg := DefaultConfig()
	if err := SetOptionValue(cfg, "scratch.width", "60"); err != nil {
		t.Fatalf("60 refused: %v", err)
	}
	if err := SetOptionValue(cfg, "scratch.height", "40%"); err != nil {
		t.Fatalf("40%% refused: %v", err)
	}
	if err := SetOptionValue(cfg, "scratch.width", ""); err != nil {
		t.Fatalf("empty refused: %v", err)
	}
	if err := SetOptionValue(cfg, "scratch.height", "tall"); err == nil {
		t.Fatal("tall accepted")
	}
}

// The default key yields to a user who already put g on another prefix
// action, instead of taking it from them.
func TestScratchDefaultKeyYields(t *testing.T) {
	cfg, err := ParseUserConfig([]byte("[keybindings.prefix_mode]\nprefix_help = [\"g\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Keybindings.PrefixMode["toggle_scratch"]; len(got) != 0 {
		t.Fatalf("toggle_scratch took g from the user's binding: %v", got)
	}
	if got := cfg.Keybindings.PrefixMode["prefix_help"]; len(got) != 1 || got[0] != "g" {
		t.Fatalf("prefix_help lost g: %v", got)
	}
}

// set-config on the old key says why, not only that the path is unknown.
func TestScratchSessionSetSaysNoLongerUsed(t *testing.T) {
	err := SetOptionValue(DefaultConfig(), "scratch.session", "notes")
	if err == nil || !strings.Contains(err.Error(), "no longer used") {
		t.Fatalf("err = %v, want the no longer used message", err)
	}
}
