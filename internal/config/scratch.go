package config

import (
	"fmt"
	"strconv"
	"strings"
)

// ScratchConfig is the [scratch] section: the session toggle_scratch shows in
// a popup over the current layout, and the size of that popup.
//
// The session is an ordinary daemon session. The popup runs `tuios attach` on
// it, so hiding the popup detaches that one client and leaves the session
// running, the way tmux-floax keeps its session.
type ScratchConfig struct {
	// Session is the name of the session the popup shows (default: scratch).
	// tuios creates it the first time it is shown.
	Session string `toml:"session"`
	// Width and Height are the popup's size, in cells (60) or percent (80%)
	// of the pane region, as tuios popup takes them (default: 80%).
	Width  string `toml:"width"`
	Height string `toml:"height"`
}

// Scratch defaults, one source for DefaultConfig, the registry and the
// accessors.
const (
	ScratchDefaultSession = "scratch"
	ScratchDefaultWidth   = "80%"
	ScratchDefaultHeight  = "80%"

	// ScratchMinWidth and ScratchMinHeight are the smallest box, border
	// included, that shows the session at its real size. The daemon counts a
	// client at no less than 20x6 cells (see session.minClientWidth), so a
	// client in a smaller pane draws a session larger than the pane, and the
	// pane cuts it off. The border takes one cell on each edge.
	ScratchMinWidth  = 20 + 2
	ScratchMinHeight = 6 + 2
)

// defaultScratchConfig returns the section DefaultConfig carries.
func defaultScratchConfig() ScratchConfig {
	return ScratchConfig{
		Session: ScratchDefaultSession,
		Width:   ScratchDefaultWidth,
		Height:  ScratchDefaultHeight,
	}
}

// fillMissingScratch fills an absent value with its default.
func fillMissingScratch(cfg, defaultCfg *UserConfig) {
	s, d := &cfg.Scratch, &defaultCfg.Scratch
	if strings.TrimSpace(s.Session) == "" {
		s.Session = d.Session
	}
	if strings.TrimSpace(s.Width) == "" {
		s.Width = d.Width
	}
	if strings.TrimSpace(s.Height) == "" {
		s.Height = d.Height
	}
}

// SessionName is the effective session name.
func (s ScratchConfig) SessionName() string {
	if name := strings.TrimSpace(s.Session); name != "" {
		return name
	}
	return ScratchDefaultSession
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

// validateScratch warns about a size that does not parse, a size in cells
// below the floor, and a session name the daemon refuses. Each one falls back
// or fails at run time, so without a warning a typo would look like the key
// ignoring the config.
func validateScratch(cfg *UserConfig, result *ValidationResult) {
	s := cfg.Scratch
	for _, f := range []struct {
		key, spec, fallback string
		floor               int
	}{
		{"width", s.Width, ScratchDefaultWidth, ScratchMinWidth},
		{"height", s.Height, ScratchDefaultHeight, ScratchMinHeight},
	} {
		if strings.TrimSpace(f.spec) == "" {
			continue
		}
		value, percent, err := ParseBoxSize(f.spec)
		switch {
		case err != nil:
			result.Warnings = append(result.Warnings, ValidationError{
				Field: "scratch", Key: f.key,
				Message: fmt.Sprintf("%v. The scratch popup uses %s.", err, f.fallback),
			})
		case !percent && value < f.floor:
			result.Warnings = append(result.Warnings, ValidationError{
				Field: "scratch", Key: f.key,
				Message: fmt.Sprintf("The scratch popup needs a %s of %d cells or more. It does not open at %d.",
					f.key, f.floor, value),
			})
		}
	}
	if msg := ScratchNameProblem(s.Session); msg != "" {
		result.Warnings = append(result.Warnings, ValidationError{
			Field: "scratch", Key: "session", Message: msg + " The scratch key cannot open it.",
		})
	}
}

// ScratchNameProblem says why a name cannot be the scratch session, or "".
// It follows session.ValidateSessionName, which config cannot import, and
// adds one rule: a name that starts with "-" reads as a flag on a command
// line. ValidateSessionName does not refuse that, because a saved session of
// such a name would then no longer restore.
func ScratchNameProblem(name string) string {
	switch {
	case name == "":
		return ""
	case strings.HasPrefix(name, "-"):
		return fmt.Sprintf("The session name %q starts with \"-\".", name)
	case strings.TrimSpace(name) != name:
		return fmt.Sprintf("The session name %q has spaces at the start or end.", name)
	case name == "." || name == "..":
		return fmt.Sprintf("The session name %q is reserved.", name)
	case strings.ContainsAny(name, `/\`):
		return fmt.Sprintf("The session name %q has a path separator.", name)
	case strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f }):
		return fmt.Sprintf("The session name %q has a control character.", name)
	}
	return ""
}
