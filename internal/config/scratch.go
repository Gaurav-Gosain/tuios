package config

import (
	"fmt"
	"strconv"
	"strings"
)

// ScratchConfig is the [scratch] section: the size of the scratch terminal,
// the one shell toggle_scratch shows in a popup over the current layout.
type ScratchConfig struct {
	// Width and Height are the popup's size, in cells (60) or percent (80%)
	// of the pane region, as tuios popup takes them (default: 80%).
	Width  string `toml:"width"`
	Height string `toml:"height"`
	// Session named the session the first design showed in the popup, a
	// nested tuios. The popup is a plain shell now, so the key is read only
	// to tell the user it is no longer used (see validateScratch). A config
	// that still has it loads.
	Session string `toml:"session,omitempty"`
}

// Scratch defaults, one source for DefaultConfig, the registry and the
// accessors.
const (
	ScratchDefaultWidth  = "80%"
	ScratchDefaultHeight = "80%"
)

// defaultScratchConfig returns the section DefaultConfig carries.
func defaultScratchConfig() ScratchConfig {
	return ScratchConfig{
		Width:  ScratchDefaultWidth,
		Height: ScratchDefaultHeight,
	}
}

// fillMissingScratch fills an absent value with its default.
func fillMissingScratch(cfg, defaultCfg *UserConfig) {
	s, d := &cfg.Scratch, &defaultCfg.Scratch
	if strings.TrimSpace(s.Width) == "" {
		s.Width = d.Width
	}
	if strings.TrimSpace(s.Height) == "" {
		s.Height = d.Height
	}
}

// WidthSpec and HeightSpec are the effective sizes. A value that does not
// parse falls back to the default, as a popup does, so a typo still shows the
// session.
func (s ScratchConfig) WidthSpec() string  { return scratchSpec(s.Width, ScratchDefaultWidth) }
func (s ScratchConfig) HeightSpec() string { return scratchSpec(s.Height, ScratchDefaultHeight) }

func scratchSpec(spec, fallback string) string {
	if _, _, err := ParseBoxSize(spec); err != nil {
		return fallback
	}
	return strings.TrimSpace(spec)
}

// ParseBoxSize reads a size in cells or percent: a bare number is cells, a
// number with a trailing percent sign is a share of the region the box sits
// in. An empty spec is not a value and is reported as such, so a caller can
// tell "the user said nothing" from "the user said 0".
//
// It is the one parser for tuios popup --width and --height and for the
// [scratch] sizes, so the two accept the same spellings.
func ParseBoxSize(spec string) (value int, percent bool, err error) {
	text := strings.TrimSpace(spec)
	if text == "" {
		return 0, false, fmt.Errorf("size is empty")
	}
	if rest, ok := strings.CutSuffix(text, "%"); ok {
		percent = true
		text = strings.TrimSpace(rest)
	}
	value, err = strconv.Atoi(text)
	if err != nil {
		return 0, percent, fmt.Errorf("%q is not a number of cells or a percentage, e.g. 60 or 60%%", spec)
	}
	if value <= 0 {
		return 0, percent, fmt.Errorf("%q is not a size, ask for at least 1", spec)
	}
	if percent && value > 100 {
		return 0, percent, fmt.Errorf("%q is more than the whole region, ask for 100%% or less", spec)
	}
	return value, percent, nil
}

// validateScratch warns about a size that does not parse and about the old
// session key. A bad size falls back at run time, so without a warning a typo
// would look like the key ignoring the config.
func validateScratch(cfg *UserConfig, result *ValidationResult) {
	s := cfg.Scratch
	for _, f := range []struct {
		key, spec, fallback string
	}{
		{"width", s.Width, ScratchDefaultWidth},
		{"height", s.Height, ScratchDefaultHeight},
	} {
		if strings.TrimSpace(f.spec) == "" {
			continue
		}
		if _, _, err := ParseBoxSize(f.spec); err != nil {
			result.Warnings = append(result.Warnings, ValidationError{
				Field: "scratch", Key: f.key,
				Message: fmt.Sprintf("%v. The scratch terminal uses %s.", err, f.fallback),
			})
		}
	}
	if strings.TrimSpace(s.Session) != "" {
		result.Warnings = append(result.Warnings, ValidationError{
			Field: "scratch", Key: "session",
			Message: ScratchSessionUnused,
		})
	}
}

// ScratchSessionUnused is what tuios says about the old [scratch] session key.
const ScratchSessionUnused = "This key is no longer used. The scratch key shows one shell in a popup. Remove the key."
