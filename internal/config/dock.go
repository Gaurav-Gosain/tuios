package config

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// The dock's built-in component names. Every segment the bar has ever drawn is
// one of these, so a user reorders the bar by reordering strings rather than by
// waiting for an option per segment.
const (
	DockComponentMode            = "mode"
	DockComponentWorkspaces      = "workspaces"
	DockComponentTrail           = "trail"
	DockComponentTape            = "tape"
	DockComponentWindows         = "windows"
	DockComponentNotifications   = "notifications"
	DockComponentCopyHelp        = "copy-help"
	DockComponentCPU             = "cpu"
	DockComponentRAM             = "ram"
	DockComponentClock           = "clock"
	DockComponentSessionControls = "session-controls"
)

// DockCustomPrefix marks a name in one of the dock lists as referring to a
// [dock.custom.NAME] table rather than to a built-in.
const DockCustomPrefix = "custom/"

// dockBuiltins is every name the lists accept without the custom/ prefix. It is
// the set the warning path checks against, so a typo says what it should have
// been instead of drawing nothing and saying nothing.
var dockBuiltins = []string{
	DockComponentMode,
	DockComponentWorkspaces,
	DockComponentTrail,
	DockComponentTape,
	DockComponentWindows,
	DockComponentNotifications,
	DockComponentCopyHelp,
	DockComponentCPU,
	DockComponentRAM,
	DockComponentClock,
	DockComponentSessionControls,
}

// DockBuiltinComponents returns the built-in component names, sorted. Used by
// the config warnings and by list-dock-components.
func DockBuiltinComponents() []string {
	out := append([]string(nil), dockBuiltins...)
	sort.Strings(out)
	return out
}

// IsDockBuiltin reports whether name is one of the dock's built-in components.
func IsDockBuiltin(name string) bool {
	for _, b := range dockBuiltins {
		if b == name {
			return true
		}
	}
	return false
}

// Default dock arrangements. These reproduce the bar exactly as it was drawn
// before the lists existed, which is what lets a session with no [dock] table
// stay byte-for-byte unchanged.
//
// Each built-in keeps whatever conditional it always had (the strip needs two
// workspaces, the meters need show_cpu), so membership here is a second gate
// and never a first one: naming a component makes it eligible to draw, not
// certain to.
var (
	defaultDockLeft   = []string{DockComponentMode, DockComponentWorkspaces, DockComponentTrail, DockComponentTape}
	defaultDockCenter = []string{DockComponentWindows}
	defaultDockRight  = []string{
		DockComponentNotifications, DockComponentCopyHelp,
		DockComponentCPU, DockComponentRAM,
		DockComponentSessionControls,
	}
)

// DefaultDockLeft, DefaultDockCenter and DefaultDockRight are the arrangement a
// config with no [dock] table gets. Copies, so a caller cannot edit the default.
func DefaultDockLeft() []string   { return append([]string(nil), defaultDockLeft...) }
func DefaultDockCenter() []string { return append([]string(nil), defaultDockCenter...) }
func DefaultDockRight() []string  { return append([]string(nil), defaultDockRight...) }

// DockConfig is the [dock] table: the bar as three ordered lists of named
// components, plus the tables that configure them.
//
// The lists are pointers because "absent" and "empty" have to stay different
// answers. A missing list takes the default arrangement; an explicitly empty one
// draws nothing on that side. The settings page rewrites the whole file through
// toml.Marshal, so both states have to survive the round trip, and a plain
// []string cannot tell them apart once it has been through it.
type DockConfig struct {
	Left   *[]string `toml:"left,omitempty"`
	Center *[]string `toml:"center,omitempty"`
	Right  *[]string `toml:"right,omitempty"`

	// Clock is [dock.clock]: the format the clock component and the status
	// badge both render. Empty means the built-in default.
	Clock DockClockConfig `toml:"clock"`

	// Custom is the set of [dock.custom.NAME] tables. A list entry of
	// "custom/NAME" draws the cell this table describes.
	Custom map[string]DockCustomConfig `toml:"custom,omitempty"`
}

// DockClockConfig is [dock.clock].
type DockClockConfig struct {
	// Format is a Go reference-time layout, e.g. "15:04" or "Mon 15:04:05".
	// The refresh cadence is derived from it: a layout showing seconds is
	// scheduled to the next second, one without to the next minute.
	Format string `toml:"format,omitempty"`
}

// DockCustomConfig is one [dock.custom.NAME] table: the whole of what a person
// has to write to put their own cell on the bar.
//
// The contract is deliberately small enough to have no version: environment
// variables in, one line of text out. There is nothing here that a future tuios
// can break, because there is no API behind it.
type DockCustomConfig struct {
	// Command is run through sh -c. Its first line of stdout is the cell.
	Command string `toml:"command"`

	// Refresh is when to run it: "once" (the default), a duration such as
	// "30s", "push" (the command stays running and each line it writes is an
	// update), or "event:TYPE[,TYPE]" (re-run when the daemon reports one).
	Refresh string `toml:"refresh,omitempty"`

	// OnClick is run through sh -c when the cell is clicked, like a hook.
	OnClick string `toml:"on-click,omitempty"`

	// MaxWidth caps the cell in cells; 0 takes DockCustomDefaultMaxWidth.
	MaxWidth int `toml:"max-width,omitempty"`
}

// Limits on what one custom component may cost. They exist because a dock cell
// is a subprocess someone else wrote: the bar has to stay a bar when it hangs,
// when it never exits, and when it writes a megabyte a second.
const (
	// DockCustomTimeout is how long a one-shot command may run before it is
	// killed and its cell hidden.
	DockCustomTimeout = 3 * time.Second

	// DockCustomMaxOutput is how much of a command's stdout is read before the
	// rest is discarded. Only the first line is ever used, so this is a cap on
	// a command that writes forever without a newline.
	DockCustomMaxOutput = 64 << 10

	// DockCustomDefaultMaxWidth is the cell width a component gets when its
	// table does not ask for one.
	DockCustomDefaultMaxWidth = 24

	// DockCustomMaxWidthLimit is the widest a cell may ask to be. A cell wider
	// than this is not a cell, it is the bar.
	DockCustomMaxWidthLimit = 80

	// DockCustomMinInterval is the floor under a polling component. Anything
	// faster is a push component that has not been written yet.
	DockCustomMinInterval = time.Second

	// DockCustomFailureLimit is how many consecutive failures a component gets
	// before it stops being re-run on its own schedule. It still runs on an
	// explicit refresh-dock, so a fixed script recovers without a restart.
	DockCustomFailureLimit = 5
)

// DockRefreshKind is how a custom component is re-run.
type DockRefreshKind int

const (
	// DockRefreshOnce runs the command at startup and then only on demand.
	DockRefreshOnce DockRefreshKind = iota
	// DockRefreshInterval polls on a deadline.
	DockRefreshInterval
	// DockRefreshPush keeps the command running and reads its lines.
	DockRefreshPush
	// DockRefreshEvent re-runs the command when a named session event lands.
	DockRefreshEvent
)

// String names the kind as it is written in config and reported by
// list-dock-components.
func (k DockRefreshKind) String() string {
	switch k {
	case DockRefreshInterval:
		return "interval"
	case DockRefreshPush:
		return "push"
	case DockRefreshEvent:
		return "event"
	default:
		return "once"
	}
}

// DockRefresh is a parsed refresh contract.
type DockRefresh struct {
	Kind     DockRefreshKind
	Interval time.Duration
	Events   []string
}

// ParseDockRefresh reads the refresh field of a [dock.custom] table.
//
// An interval below the floor is raised to it rather than rejected: the value
// the user wanted is obvious, and a dock that refuses to start over "0.1s" is
// worse than one that polls a little slower than asked.
func ParseDockRefresh(s string) (DockRefresh, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "" || s == "once":
		return DockRefresh{Kind: DockRefreshOnce}, nil
	case s == "push":
		return DockRefresh{Kind: DockRefreshPush}, nil
	case strings.HasPrefix(s, "event:"):
		var events []string
		for _, e := range strings.Split(strings.TrimPrefix(s, "event:"), ",") {
			if e = strings.TrimSpace(e); e != "" {
				events = append(events, e)
			}
		}
		if len(events) == 0 {
			return DockRefresh{}, fmt.Errorf("refresh %q names no event type", s)
		}
		return DockRefresh{Kind: DockRefreshEvent, Events: events}, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return DockRefresh{}, fmt.Errorf("refresh %q is not once, push, event:TYPE or a duration", s)
	}
	if d < DockCustomMinInterval {
		d = DockCustomMinInterval
	}
	return DockRefresh{Kind: DockRefreshInterval, Interval: d}, nil
}

// DockClockFormat is the layout the clock renders in, falling back to the
// format the status badge has always used.
func (c DockConfig) DockClockFormat() string {
	if f := strings.TrimSpace(c.Clock.Format); f != "" {
		return f
	}
	return DefaultClockFormat
}

// DefaultClockFormat is what the clock showed before it was configurable. Kept
// exact so a session that never opens the [dock.clock] table sees no change.
const DefaultClockFormat = "15:04:05"

// DockClockInterval is how often a clock in this format has to be redrawn. A
// layout without seconds moves once a minute, and asking it to move sixty times
// a second was the whole of the old clock's cost.
func DockClockInterval(format string) time.Duration {
	if clockFormatHasSeconds(format) {
		return time.Second
	}
	return time.Minute
}

// clockFormatHasSeconds reports whether a Go time layout renders seconds.
// "05" is the reference second and ".000"/".999" its fractions; nothing else in
// a layout moves faster than a minute.
func clockFormatHasSeconds(format string) bool {
	return strings.Contains(format, "05") || strings.Contains(format, ".0") ||
		strings.Contains(format, ".9") || strings.Contains(format, "5.")
}

// DockList returns the components on one side, resolving an absent list to the
// default arrangement for that side.
func (c DockConfig) DockList(side string) []string {
	switch side {
	case "left":
		if c.Left != nil {
			return *c.Left
		}
		return DefaultDockLeft()
	case "center":
		if c.Center != nil {
			return *c.Center
		}
		return DefaultDockCenter()
	default:
		if c.Right != nil {
			return *c.Right
		}
		return DefaultDockRight()
	}
}

// validateDock appends the dock section's problems to result. Every one of them
// is a warning: a mistyped component name should cost the user that cell, not
// the session, and the config's own error path refuses to start at all.
//
// The lesson here was learned on keybindings, where an action typo silently
// bound nothing and looked exactly like a broken feature.
func validateDock(cfg *UserConfig, result *ValidationResult) {
	seen := map[string]string{}
	for _, side := range []string{"left", "center", "right"} {
		list := cfg.Dock.DockList(side)
		for _, name := range list {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			if where, dup := seen[name]; dup {
				result.Warnings = append(result.Warnings, ValidationError{
					Field:   "dock." + side,
					Key:     name,
					Message: fmt.Sprintf("component is already on the %s; the second copy is ignored", where),
				})
				continue
			}
			seen[name] = side

			if custom, ok := strings.CutPrefix(name, DockCustomPrefix); ok {
				spec, defined := cfg.Dock.Custom[custom]
				if !defined {
					result.Warnings = append(result.Warnings, ValidationError{
						Field:   "dock." + side,
						Key:     name,
						Message: fmt.Sprintf("no [dock.custom.%s] table defines this component", custom),
					})
					continue
				}
				if strings.TrimSpace(spec.Command) == "" {
					result.Warnings = append(result.Warnings, ValidationError{
						Field:   "dock.custom." + custom,
						Key:     "command",
						Message: "component has no command, so it can never draw anything",
					})
				}
				continue
			}
			if !IsDockBuiltin(name) {
				result.Warnings = append(result.Warnings, ValidationError{
					Field: "dock." + side,
					Key:   name,
					Message: fmt.Sprintf("unknown component; built-ins are %s, and a custom one is written custom/NAME",
						strings.Join(DockBuiltinComponents(), ", ")),
				})
			}
		}
	}

	for name, spec := range cfg.Dock.Custom {
		if _, err := ParseDockRefresh(spec.Refresh); err != nil {
			result.Warnings = append(result.Warnings, ValidationError{
				Field:   "dock.custom." + name,
				Key:     "refresh",
				Message: err.Error() + "; the component falls back to once",
			})
		}
		if spec.MaxWidth < 0 || spec.MaxWidth > DockCustomMaxWidthLimit {
			result.Warnings = append(result.Warnings, ValidationError{
				Field: "dock.custom." + name,
				Key:   "max-width",
				Message: fmt.Sprintf("must be between 1 and %d; using the default of %d",
					DockCustomMaxWidthLimit, DockCustomDefaultMaxWidth),
			})
		}
		// A table nobody placed is the other half of the typo: the script is
		// right, the list entry is missing, and the cell never appears.
		if _, placed := seen[DockCustomPrefix+name]; !placed {
			result.Warnings = append(result.Warnings, ValidationError{
				Field: "dock.custom." + name,
				Key:   name,
				Message: fmt.Sprintf("defined but not on any dock list; add %q to dock.left, dock.center or dock.right",
					DockCustomPrefix+name),
			})
		}
	}
}
