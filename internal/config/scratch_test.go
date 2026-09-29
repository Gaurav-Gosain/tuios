package config

import (
	"strings"
	"testing"
)

func TestScratchDefaults(t *testing.T) {
	s := DefaultConfig().Scratch
	if s.SessionName() != "scratch" || s.WidthSpec() != "80%" || s.HeightSpec() != "80%" {
		t.Fatalf("defaults = %+v", s)
	}
	if got := DefaultConfig().Keybindings.PrefixMode["toggle_scratch"]; len(got) != 1 || got[0] != "g" {
		t.Fatalf("toggle_scratch default key = %v, want [g]", got)
	}
}

func TestScratchTableParses(t *testing.T) {
	cfg, err := ParseUserConfig([]byte("[scratch]\nsession = \"notes\"\nwidth = \"100\"\nheight = \"50%\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	s := cfg.Scratch
	if s.SessionName() != "notes" || s.WidthSpec() != "100" || s.HeightSpec() != "50%" {
		t.Fatalf("parsed = %+v", s)
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
	if s.Session != "scratch" || s.Width != "70%" || s.Height != "80%" {
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
		{"cells", ScratchConfig{Session: "s", Width: "100", Height: "30"}, ""},
		{"percent below floor is fine", ScratchConfig{Session: "s", Width: "5%", Height: "5%"}, ""},
		{"not a size", ScratchConfig{Session: "s", Width: "wide"}, "is not a number"},
		{"too many percent", ScratchConfig{Session: "s", Height: "120%"}, "more than the whole region"},
		{"narrow cells", ScratchConfig{Session: "s", Width: "10"}, "width of 22 cells or more"},
		{"short cells", ScratchConfig{Session: "s", Height: "5"}, "height of 8 cells or more"},
		{"path in name", ScratchConfig{Session: "a/b"}, "path separator"},
		{"space in name", ScratchConfig{Session: " s"}, "spaces at the start or end"},
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
