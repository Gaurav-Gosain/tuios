package config

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
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
	// DockComponentTransfers is the copies between machines: a bar and a
	// percent while a copy runs. It draws nothing while no copy exists.
	DockComponentTransfers = "transfers"
	// DockComponentSession is the session name with the current workspace and
	// the focused pane's place in it, "main 2:1" by default. It is a cell, so
	// it goes wherever it is listed, and it is in no default list.
	DockComponentSession = "session"
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
	DockComponentTransfers,
	DockComponentSession,
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
	return slices.Contains(dockBuiltins, name)
}

// dockFixedSides names the built-ins that are drawn on one side whatever list
// they are written in.
//
// They are not cells. The minimized entries are the centre block and are
// measured against the room the two ends leave; the message and the copy-mode
// help are transient claims on the right-hand end rather than segments; and the
// session controls hold the bar's last columns and are never truncated. Listing
// one of them elsewhere is taken as "draw it", not as "draw it there", because
// the alternative is a segment that reserves width on one side and draws on
// another.
var dockFixedSides = map[string]string{
	DockComponentWindows:         "center",
	DockComponentNotifications:   "right",
	DockComponentCopyHelp:        "right",
	DockComponentSessionControls: "right",
}

// DockFixedSide is the side a component is always drawn on, or "" when it goes
// wherever it is listed.
func DockFixedSide(name string) string { return dockFixedSides[name] }

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
		DockComponentTransfers,
		DockComponentNotifications, DockComponentCopyHelp,
		DockComponentCPU, DockComponentRAM, DockComponentClock,
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

	// Tabs is [dock.tabs]: how the workspace strip draws its tabs.
	Tabs DockTabsConfig `toml:"tabs,omitempty"`

	// Session is [dock.session]: the "session" component's text and style.
	Session DockSessionConfig `toml:"session,omitempty"`
}

// Workspace tab styles accepted by [dock.tabs] style.
const (
	DockTabStylePill = "pill"
	DockTabStyleFlat = "flat"
)

// DockTabsConfig is [dock.tabs]. With style left empty (or "pill") the strip
// draws its rounded pills as it always has and the other fields are ignored;
// "flat" draws each tab as a coloured run of text, the tabs joined by
// Separator, the way a tmux status line draws its window list.
type DockTabsConfig struct {
	Style string `toml:"style,omitempty"`

	// Separator is drawn between two flat tabs. Empty is one bare column.
	Separator   string `toml:"separator,omitempty"`
	SeparatorFg string `toml:"separator_fg,omitempty"`

	ActiveFg     string `toml:"active_fg,omitempty"`
	ActiveBg     string `toml:"active_bg,omitempty"`
	ActiveBold   bool   `toml:"active_bold,omitempty"`
	InactiveFg   string `toml:"inactive_fg,omitempty"`
	InactiveBg   string `toml:"inactive_bg,omitempty"`
	InactiveBold bool   `toml:"inactive_bold,omitempty"`

	// NumberGlyphs is what {icon} in appearance.dock_workspace_tab_format
	// prints for each workspace: the first entry stands for workspace 1, and
	// a workspace past the end prints its number.
	NumberGlyphs []string `toml:"number_glyphs,omitempty"`

	// ZoomMarker is what {zoom} prints on the current workspace's tab while a
	// pane in it is zoomed.
	ZoomMarker string `toml:"zoom_marker,omitempty"`

	// ShowAdd keeps the trailing "+" tab in the flat style, which drops it by
	// default. The pill style always draws it.
	ShowAdd bool `toml:"show_add,omitempty"`

	// AttentionFg is the ink of a workspace's tab while a pane on it waits
	// for a person: an agent that asks for input or stopped on an error. A
	// colour value; empty leaves the tab's ink alone.
	AttentionFg string `toml:"attention_fg,omitempty"`
	// BellMarker is put after the label of a workspace whose pane rang the
	// bell since you last looked at it, and ActivityMarker after one whose
	// pane printed. Empty draws none, and tuios then keeps no record of it.
	BellMarker     string `toml:"bell_marker,omitempty"`
	ActivityMarker string `toml:"activity_marker,omitempty"`
}

// Flat reports whether the strip draws flat tabs.
func (t DockTabsConfig) Flat() bool { return strings.TrimSpace(t.Style) == DockTabStyleFlat }

// Icon is what {icon} prints for workspace n.
func (t DockTabsConfig) Icon(n int) string {
	if n >= 1 && n <= len(t.NumberGlyphs) && t.NumberGlyphs[n-1] != "" {
		return t.NumberGlyphs[n-1]
	}
	return strconv.Itoa(n)
}

// DockCellStyle colours one dock cell. Every field is optional; a cell with
// none set draws on the dock's quiet Panel ground as it always has. Colours are
// "#rrggbb". The caps are drawn in the cell's background colour either side of
// it, which is how a half-circle glyph reads as the cell's rounded end.
type DockCellStyle struct {
	Fg       string `toml:"fg,omitempty"`
	Bg       string `toml:"bg,omitempty"`
	Bold     bool   `toml:"bold,omitempty"`
	CapLeft  string `toml:"cap_left,omitempty"`
	CapRight string `toml:"cap_right,omitempty"`
}

// DockSessionConfig is [dock.session].
type DockSessionConfig struct {
	DockCellStyle

	// Format is the cell's text. Placeholders are {session} (the session name),
	// {workspace} (the current workspace number) and {pane} (the focused pane's
	// 1-based place in that workspace). Empty takes DefaultDockSessionFormat.
	Format string `toml:"format,omitempty"`
}

// DefaultDockSessionFormat is the session component's text when [dock.session]
// gives no format.
const DefaultDockSessionFormat = "{session} {workspace}:{pane}"

// FormatDockSession renders the session component's text. A format that
// starts or ends with an empty placeholder is trimmed, so a session with no
// name does not leave a dangling space.
func FormatDockSession(format, session string, workspace, pane int) string {
	if format == "" {
		format = DefaultDockSessionFormat
	}
	return strings.TrimSpace(strings.NewReplacer(
		"{session}", session,
		"{workspace}", strconv.Itoa(workspace),
		"{pane}", strconv.Itoa(pane),
	).Replace(format))
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

	// The cell's colours and caps; see DockCellStyle.
	DockCellStyle
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

// dockEventTypes is every event a refresh contract may watch. Both spellings
// are here: the hook table names the event after-focus-change and the daemon's
// event hub names the same thing window-focused, and a component author should
// not have to know which of the two they are talking to.
//
// The list lives in this package rather than beside the alias map in
// internal/app so the config warning can check a name at load, which is the
// only moment anyone is looking. An event a component watches and nothing ever
// fires is a cell that never updates and says nothing about why.
var dockEventTypes = []string{
	"after-agent-state", "after-attach", "after-close-window", "after-detach",
	"after-focus-change", "after-layout-change", "after-new-window",
	"after-resize", "after-theme-change", "after-workspace-switch",
	"agent-state", "attached", "detached", "layout-changed", "resized",
	"theme-changed", "window-closed", "window-created", "window-focused", "workspace-switched",
}

// DockEventTypes returns the event names a refresh contract may watch, sorted.
func DockEventTypes() []string {
	return append([]string(nil), dockEventTypes...)
}

// IsDockEventType reports whether name is an event a component may watch.
func IsDockEventType(name string) bool {
	return slices.Contains(dockEventTypes, name)
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
		for e := range strings.SplitSeq(strings.TrimPrefix(s, "event:"), ",") {
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
func (c DockConfig) DockClockFormat(s *Settings) string {
	if f := strings.TrimSpace(c.Clock.Format); f != "" {
		return f
	}
	// Falls through to appearance.clock_format rather than straight to the
	// default: the two settings arrived by different doors for the same
	// question, and a session that set the appearance one and never opened
	// [dock.clock] would otherwise watch it do nothing. The specific table
	// still wins over the general one when it says anything at all.
	return s.GetClockFormat()
}

// DockClockInterval is how often a clock in this format has to be redrawn. A
// layout without seconds moves once a minute, and asking it to move sixty times
// a second would be the whole of the clock's cost.
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
				continue
			}
			if fixed := DockFixedSide(name); fixed != "" && fixed != side {
				result.Warnings = append(result.Warnings, ValidationError{
					Field: "dock." + side,
					Key:   name,
					Message: fmt.Sprintf("this component is always drawn on the %s; it is still shown, but not here",
						fixed),
				})
			}
		}
	}

	if st := strings.TrimSpace(cfg.Dock.Tabs.Style); st != "" && st != DockTabStylePill && st != DockTabStyleFlat {
		result.Warnings = append(result.Warnings, ValidationError{
			Field: "dock.tabs", Key: "style",
			Message: fmt.Sprintf("style %q is not %s or %s; drawing pills", st, DockTabStylePill, DockTabStyleFlat),
		})
	}
	t := cfg.Dock.Tabs
	validateDockColors(result, "dock.tabs", map[string]string{
		"separator_fg": t.SeparatorFg, "active_fg": t.ActiveFg, "active_bg": t.ActiveBg,
		"inactive_fg": t.InactiveFg, "inactive_bg": t.InactiveBg,
		"attention_fg": t.AttentionFg,
	})
	validateDockColors(result, "dock.session", map[string]string{
		"fg": cfg.Dock.Session.Fg, "bg": cfg.Dock.Session.Bg,
	})

	for name, spec := range cfg.Dock.Custom {
		validateDockColors(result, "dock.custom."+name, map[string]string{"fg": spec.Fg, "bg": spec.Bg})
		refresh, err := ParseDockRefresh(spec.Refresh)
		switch {
		case err != nil:
			result.Warnings = append(result.Warnings, ValidationError{
				Field:   "dock.custom." + name,
				Key:     "refresh",
				Message: err.Error() + "; the component falls back to once",
			})
		case refresh.Kind == DockRefreshEvent:
			for _, event := range refresh.Events {
				if IsDockEventType(event) {
					continue
				}
				result.Warnings = append(result.Warnings, ValidationError{
					Field: "dock.custom." + name,
					Key:   "refresh",
					Message: fmt.Sprintf("nothing ever fires %q, so this component would never refresh; the events are %s",
						event, strings.Join(DockEventTypes(), ", ")),
				})
			}
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

// validateDockColors warns about every set colour that is not a colour (see
// theme.ParseColor). The value is dropped where it is drawn, so the cell still
// draws, without that colour.
func validateDockColors(result *ValidationResult, field string, colors map[string]string) {
	keys := make([]string, 0, len(colors))
	for k := range colors {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	kv := make([]string, 0, 2*len(keys))
	for _, k := range keys {
		kv = append(kv, k, colors[k])
	}
	warnColours(result, field, "The dock ignores it", kv...)
}
