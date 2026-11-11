package config

import (
	"log"
	"os"
	"strconv"
	"sync/atomic"

	"github.com/Gaurav-Gosain/tuios/internal/theme"
)

// OptionSet is one option value set from outside the config file: a CLI
// flag, or an option of an embedding program. It goes through
// SetOptionValue, so it is checked as a value sent with set-config is.
type OptionSet struct {
	Path  string
	Value string
}

// Overrides are the values a program lays over the config file for one run:
// the CLI flags, or an embedder's options. They change the settings a
// session starts from, never the loaded config, which the settings page
// saves back to the file.
type Overrides struct {
	// Sets are option values laid over the file before the settings are
	// resolved, in order. See Config.
	Sets []OptionSet

	// ASCIIOnly uses ASCII characters instead of Nerd Font icons.
	ASCIIOnly bool

	// NoAnimations turns every animation off for this run.
	NoAnimations bool

	// ThemeName is the theme to load.
	ThemeName string
}

// Config is cfg with the Sets applied, as a copy, so the caller's config
// keeps the file's values. SetOptionValue allocates every pointer field it
// writes, so the copy shares no value it changes with cfg. A set that
// fails, which a flag cannot do because it is checked when it is parsed, is
// logged and skipped. With no Sets it is cfg itself.
func (ov Overrides) Config(cfg *UserConfig) *UserConfig {
	if len(ov.Sets) == 0 {
		return cfg
	}
	if cfg == nil {
		cfg = DefaultConfig()
	}
	c := *cfg
	for _, set := range ov.Sets {
		if err := SetOptionValue(&c, set.Path, set.Value); err != nil {
			log.Printf("Warning: %v", err)
		}
	}
	return &c
}

// EnvSets are the options the environment sets: one for each option with an
// Env whose variable is set and not empty, in registry order.
func EnvSets() []OptionSet {
	var sets []OptionSet
	for _, opt := range optionSpecs {
		if opt.Env == "" {
			continue
		}
		if v := os.Getenv(opt.Env); v != "" {
			sets = append(sets, OptionSet{Path: opt.Path, Value: v})
		}
	}
	return sets
}

// WithEnv is cfg with the environment's options laid over it, as Config lays
// a run's Sets: a copy when a variable is set, cfg itself when none is. A
// value the option does not take is logged and skipped, so the file's value
// stands.
func WithEnv(cfg *UserConfig) *UserConfig {
	return Overrides{Sets: EnvSets()}.Config(cfg)
}

// IntValue is the int option at path as a reader uses it: 0 under
// ZeroIsDefault reads as the default, and a value outside Min to Max moves to
// the nearer end. A nil cfg is the default config. A path the registry does
// not carry reads as 0.
func IntValue(cfg *UserConfig, path string) int {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	opt, ok := LookupOption(path)
	if !ok {
		return 0
	}
	text, _ := GetOptionValue(cfg, path)
	if _, err := strconv.Atoi(text); err != nil {
		return 0
	}
	return opt.resolveInt(text)
}

// themeFlag is the last --theme name ApplyOverrides applied, or "".
// ApplyAppearanceConfig uses it for a config file that sets no theme.
var themeFlag atomic.Pointer[string]

// FlagTheme is the theme the --theme flag named, or "". An explicit flag wins
// over appearance.theme_light.
func FlagTheme() string { return flagTheme() }

func flagTheme() string {
	if p := themeFlag.Load(); p != nil {
		return *p
	}
	return ""
}

// ApplyOverrides applies the overrides that are not config options: ASCII
// glyphs, no animations and the theme. The Sets are applied before the
// settings are resolved, through Config, so every entrypoint resolves with
// ApplyAppearanceConfig(ov.Config(cfg), s) and then calls this.
func ApplyOverrides(overrides Overrides, s *Settings) {
	if overrides.ASCIIOnly {
		s.ASCIIRequested = true
		s.UseASCIIOnly = true
	}
	if overrides.NoAnimations {
		s.NoAnimationsFlag = true
		s.Motion = MotionNone
	}
	if overrides.ThemeName != "" {
		themeFlag.Store(&overrides.ThemeName)
		if err := theme.Initialize(overrides.ThemeName); err != nil {
			log.Printf("Warning: Failed to load theme '%s': %v", overrides.ThemeName, err)
		}
	}
}

// Resolve is the Settings a session starts from: the built-in defaults, the
// config file, then the run's overrides. It is the one constructor of
// Settings: every entrypoint calls it (or ResolveFrom) and hands the result to
// app.NewOS, and a served session calls it per connection, so a session that
// connects after an edit follows the file. A nil cfg is the default config,
// so the overrides still apply.
func Resolve(cfg *UserConfig, ov Overrides) Settings {
	return ResolveFrom(DefaultSettings(), cfg, ov)
}

// ResolveFrom is Resolve starting from seed instead of the built-in defaults,
// for a caller that knows something about the terminal first, such as its
// glyph environment (see DetectGlyphEnv).
func ResolveFrom(seed Settings, cfg *UserConfig, ov Overrides) Settings {
	s := seed
	if cfg != nil || len(ov.Sets) > 0 {
		ApplyAppearanceConfig(ov.Config(cfg), &s)
	}
	ApplyOverrides(ov, &s)
	return s
}
