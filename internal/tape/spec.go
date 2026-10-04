package tape

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// argSpec is what one command takes: how many arguments, and a check of their
// values. The parser holds every command to its spec, so a tape that passes
// validate runs every argument it was given. Before this, most commands
// dropped what followed them without a word: Split horizontal parsed and then
// failed at run time for want of a direction, and NewWindow "editor" opened an
// unnamed window.
type argSpec struct {
	min, max int // max < 0 is no limit
	usage    string
	check    func(args []string) (badArg int, err error)
}

// unlimited marks an argSpec with no upper bound.
const unlimited = -1

// keyRepeat is the optional count a key command takes: Down 5.
var keyRepeat = argSpec{0, 1, "an optional count, as in Down 5", checkEach(checkPositiveInt)}

// noArgs is a command that takes nothing.
var noArgs = argSpec{0, 0, "no arguments", nil}

// argSpecs is every command the generic parser handles. Type, Sleep, Wait,
// WaitUntilRegex and key combos have parsers of their own.
var argSpecs = map[CommandType]argSpec{
	CommandTypeEnter: keyRepeat, CommandTypeSpace: keyRepeat, CommandTypeBackspace: keyRepeat,
	CommandTypeDelete: keyRepeat, CommandTypeTab: keyRepeat, CommandTypeEscape: keyRepeat,
	CommandTypeUp: keyRepeat, CommandTypeDown: keyRepeat, CommandTypeLeft: keyRepeat,
	CommandTypeRight: keyRepeat, CommandTypeHome: keyRepeat, CommandTypeEnd: keyRepeat,

	CommandTypeTerminalMode: noArgs, CommandTypeWindowManagementMode: noArgs,
	CommandTypeNextWindow: noArgs, CommandTypePrevWindow: noArgs,
	CommandTypeToggleTiling: noArgs, CommandTypeEnableTiling: noArgs, CommandTypeDisableTiling: noArgs,
	CommandTypeSnapLeft: noArgs, CommandTypeSnapRight: noArgs, CommandTypeSnapFullscreen: noArgs,
	CommandTypeRotateSplit: noArgs, CommandTypeEqualizeSplits: noArgs, CommandTypeToggleZoom: noArgs,
	CommandTypeScreenshot: noArgs, CommandTypeSmartSplit: noArgs, CommandTypeCommandPalette: noArgs,
	CommandTypeEnableAnimations: noArgs, CommandTypeDisableAnimations: noArgs, CommandTypeToggleAnimations: noArgs,
	CommandTypeCycleMasterPosition: noArgs, CommandTypeAddMaster: noArgs, CommandTypeRemoveMaster: noArgs,
	CommandTypeSwapWithMaster: noArgs, CommandTypeFocusMaster: noArgs,

	CommandTypeNewWindow:      {0, 1, "an optional window name", nil},
	CommandTypeCloseWindow:    {0, 1, "an optional window name", nil},
	CommandTypeMinimizeWindow: {0, 1, "an optional window name", nil},
	CommandTypeRestoreWindow:  {0, 1, "an optional window name", nil},
	CommandTypeFocusWindow:    {1, 1, "a window name or id", nil},
	CommandTypeFocus:          {1, 1, "a window name or id", nil},
	CommandTypeRenameWindow:   {1, 2, `a new name, or the old name and the new one`, nil},

	CommandTypeSplit:          {1, 1, "horizontal or vertical", checkEach(checkOneOf("horizontal", "vertical", "h", "v"))},
	CommandTypePreselect:      {1, 1, "left, right, up or down", checkEach(checkOneOf("left", "right", "up", "down"))},
	CommandTypeFocusDirection: {1, 1, "left, right, up or down", checkEach(checkOneOf("left", "right", "up", "down"))},
	CommandTypeArrangePanes: {1, unlimited, "tiled, even-horizontal or even-vertical, then optional window names", func(args []string) (int, error) {
		return 0, checkOneOf("tiled", "even-horizontal", "even-vertical")(args[0])
	}},
	CommandTypeSetMultifocus:     {0, unlimited, "the window names, or none to clear the set", nil},
	CommandTypeSetMasterPosition: {1, 1, "left, right, top, bottom or center", checkEach(checkOneOf("left", "right", "top", "bottom", "center"))},
	CommandTypeSetMasterCount:    {1, 1, "a number of master panes", checkEach(checkPositiveInt)},

	CommandTypeSwitchWS:        {1, 1, "a workspace number", checkEach(checkPositiveInt)},
	CommandTypeMoveToWS:        {1, 1, "a workspace number", checkEach(checkPositiveInt)},
	CommandTypeMoveAndFollowWS: {1, 1, "a workspace number", checkEach(checkPositiveInt)},

	CommandTypeSaveLayout:         {1, 1, "a layout name", nil},
	CommandTypeLoadLayout:         {1, 1, "a layout name", nil},
	CommandTypeSet:                {2, 2, "a config path and a value, as in Set appearance.border_style rounded", nil},
	CommandTypeSetConfig:          {2, 2, "a config path and a value", nil},
	CommandTypeSetTheme:           {1, 1, "a theme name", nil},
	CommandTypeSetDockbarPosition: {1, 1, "top, bottom or hidden", nil},
	CommandTypeSetBorderStyle:     {1, 1, "a border style name", nil},
	CommandTypeShowNotification:   {1, 2, "a message and an optional type: info, success, warning or error", nil},
	CommandTypeOutput: {1, 1, "a file name", func([]string) (int, error) {
		return -1, fmt.Errorf("tuios does not render tapes to a file, so Output does nothing. Remove the line, or use Screenshot")
	}},
	CommandTypeSource: {1, 1, "a tape file to include", nil},

	CommandTypeAction:  {1, 1, "an action name, as in Action toggle_spotlight", checkEach(checkAction)},
	CommandTypePress:   {1, unlimited, `keys, as in Press "ctrl+b q"`, nil},
	CommandTypeRun:     {1, 1, `a command line, as in Run "make test"`, nil},
	CommandTypeWaitFor: {1, unlimited, `a condition, as in WaitFor text "ready" 10s`, checkCondition(CommandTypeWaitFor)},
	CommandTypeExpect:  {1, unlimited, `a condition, as in Expect text "ready"`, checkCondition(CommandTypeExpect)},
}

// checkEach applies one check to every argument.
func checkEach(check func(string) error) func([]string) (int, error) {
	return func(args []string) (int, error) {
		for i, a := range args {
			if err := check(a); err != nil {
				return i, err
			}
		}
		return 0, nil
	}
}

func checkPositiveInt(s string) error {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return fmt.Errorf("%q is not a positive whole number", s)
	}
	return nil
}

func checkOneOf(values ...string) func(string) error {
	return func(s string) error {
		for _, v := range values {
			if strings.EqualFold(s, v) {
				return nil
			}
		}
		return fmt.Errorf("%q is not one of %s", s, strings.Join(values, ", "))
	}
}

// checkAction accepts the name of a keybinding action, or of a
// [[keybindings.command]] entry, whose names come from the config the tape
// runs under and so cannot be checked here.
func checkAction(name string) error {
	if IsActionName(name) {
		return nil
	}
	return fmt.Errorf("%q is not an action. Run 'tuios keybinds list' for the action names", name)
}

// IsActionName reports whether name is a keybinding action a tape may run:
// one with a description, one the key handling registered (see
// RegisterActions), or a [[keybindings.command]] entry.
func IsActionName(name string) bool {
	if strings.HasPrefix(name, config.CommandActionPrefix) && len(name) > len(config.CommandActionPrefix) {
		return true
	}
	if _, ok := config.ActionDescriptions[name]; ok {
		return true
	}
	_, ok := registeredActions.Load(name)
	return ok
}

// registeredActions are the action names the key handling dispatches. Some
// have no description (the scrolling layout's scroll_*, next_workspace, the
// prefix copies of window actions), and a key bound to one in config.toml
// runs it, so a tape may too.
var registeredActions sync.Map

// RegisterActions adds names to the actions a tape may run. internal/input
// registers its dispatcher's names when it loads.
func RegisterActions(names ...string) {
	for _, n := range names {
		registeredActions.Store(n, struct{}{})
	}
}

func checkCondition(ct CommandType) func([]string) (int, error) {
	return func(args []string) (int, error) {
		_, err := ParseCondition(&Command{Type: ct, Args: args})
		return 0, err
	}
}

// checkArgs holds cmd to its spec. It returns the index of the argument at
// fault, or -1 when the fault is the command as a whole.
func checkArgs(cmd *Command) (int, error) {
	spec, ok := argSpecs[cmd.Type]
	if !ok {
		return -1, nil
	}
	n := len(cmd.Args)
	if n < spec.min {
		return -1, fmt.Errorf("%s needs %s", cmd.Type, spec.usage)
	}
	if spec.max != unlimited && n > spec.max {
		if spec.max == 0 {
			return spec.max, fmt.Errorf("%s takes no arguments", cmd.Type)
		}
		return spec.max, fmt.Errorf("%s takes %s, and %q is one too many", cmd.Type, spec.usage, cmd.Args[spec.max])
	}
	if spec.check != nil && n > 0 {
		if i, err := spec.check(cmd.Args); err != nil {
			if strings.HasPrefix(err.Error(), string(cmd.Type)) {
				return i, err
			}
			return i, fmt.Errorf("%s: %v", cmd.Type, err)
		}
	}
	return -1, nil
}

// Condition is what WaitFor waits for and Expect checks:
//
//	text "pattern" [in "pane"]   the pane's screen matches the regular expression
//	pane "name"                  a pane with this name or id exists
//	gone "name"                  no pane has this name or id
//	focus "name"                 the focused pane has this name or id
//	panes N                      the current workspace has N panes
//	agent "state" [in "pane"]    the pane's agent reports this state
//	workspace N                  workspace N is the one on screen
//	mode terminal|window         tuios is in this mode
//
// Without "in", text and agent read the focused pane. WaitFor takes an
// optional timeout after the condition, as a duration: WaitFor text "ok" 30s.
type Condition struct {
	Kind    string
	Value   string
	Pattern *regexp.Regexp // Kind "text"
	Count   int            // Kind "panes" and "workspace"
	Pane    string         // the pane after "in", empty for the focused one
	Timeout time.Duration  // WaitFor only
}

// DefaultWaitTimeout is how long WaitFor waits when the tape gives no timeout.
const DefaultWaitTimeout = 10 * time.Second

// conditionKinds are the kinds a Condition can have, and whether each reads a
// pane "in" may name.
var conditionKinds = map[string]bool{
	"text": true, "agent": true,
	"pane": false, "gone": false, "focus": false, "panes": false, "workspace": false, "mode": false,
}

// ParseCondition reads the condition of a WaitFor or an Expect command.
func ParseCondition(cmd *Command) (Condition, error) {
	var c Condition
	if len(cmd.Args) == 0 {
		return c, fmt.Errorf("%s needs a condition: text, pane, gone, focus, panes, agent, workspace or mode", cmd.Type)
	}
	c.Kind = strings.ToLower(cmd.Args[0])
	takesPane, ok := conditionKinds[c.Kind]
	if !ok {
		return c, fmt.Errorf("%q is not a condition. Use text, pane, gone, focus, panes, agent, workspace or mode", cmd.Args[0])
	}
	if cmd.Type == CommandTypeWaitFor {
		c.Timeout = DefaultWaitTimeout
	}
	var values []string
	rest := cmd.Args[1:]
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch {
		case strings.EqualFold(a, "in") && i+1 < len(rest):
			if !takesPane {
				return c, fmt.Errorf("%s %s does not take a pane", cmd.Type, c.Kind)
			}
			c.Pane = rest[i+1]
			i++
		case isDuration(a):
			if cmd.Type != CommandTypeWaitFor {
				return c, fmt.Errorf("%s checks once and takes no timeout. Use WaitFor to wait", cmd.Type)
			}
			d, _ := time.ParseDuration(a)
			if d <= 0 {
				return c, fmt.Errorf("the timeout %q must be more than zero", a)
			}
			c.Timeout = d
		default:
			values = append(values, a)
		}
	}
	if len(values) != 1 {
		return c, fmt.Errorf("%s %s needs one value, got %d", cmd.Type, c.Kind, len(values))
	}
	c.Value = values[0]
	switch c.Kind {
	case "text":
		re, err := regexp.Compile(c.Value)
		if err != nil {
			return c, fmt.Errorf("the pattern %q is not a regular expression: %v", c.Value, err)
		}
		c.Pattern = re
	case "panes", "workspace":
		n, err := strconv.Atoi(c.Value)
		if err != nil || n < 0 {
			return c, fmt.Errorf("%s %s needs a number, got %q", cmd.Type, c.Kind, c.Value)
		}
		c.Count = n
	case "mode":
		v := strings.ToLower(c.Value)
		if v != "terminal" && v != "window" {
			return c, fmt.Errorf("%s mode needs terminal or window, got %q", cmd.Type, c.Value)
		}
		c.Value = v
	}
	return c, nil
}

// String spells the condition the way a tape writes it.
func (c Condition) String() string {
	s := c.Kind + " " + strconv.Quote(c.Value)
	if c.Pane != "" {
		s += " in " + strconv.Quote(c.Pane)
	}
	return s
}

// isDuration reports whether s is a duration with a unit, such as 500ms or
// 10s. A bare number is not one: in "panes 3" the 3 is the value.
func isDuration(s string) bool {
	if s == "" || !isDigit(s[0]) {
		return false
	}
	_, err := time.ParseDuration(s)
	return err == nil
}
