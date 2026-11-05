package main

import (
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/spf13/cobra"
)

// The layout commands: split, zoom, swap and resize a named pane. The daemon
// makes each change itself, so they work with no client attached. Without
// -w, a command run in a pane acts on that pane, and one run outside every
// pane acts on the focused pane.

func newSplitWindowCommand() *cobra.Command {
	var sessionName, window, name, cwd string
	var ratio int
	var noFocus, keepOpen, jsonOutput bool
	cmd := &cobra.Command{
		Use:   "split-window <left|right|up|down|horizontal|vertical> [command...]",
		Short: "Divide a pane and open a new one beside it",
		Long: `Split a pane and print the id of the new pane. The side is where the new
pane goes. horizontal and vertical put it below or to the right.

Words after the side are the command the new pane runs, as for new-window.
Without -w, the command splits the pane you run it in, or the focused pane
outside every pane. Works with no client attached, and turns tiling on.`,
		Example: `  tuios split-window right
  tuios split-window down -w build --ratio 30 --no-focus -- tail -f app.log`,
		Args:      cobra.MinimumNArgs(1),
		ValidArgs: []string{"horizontal", "vertical", "left", "right", "up", "down"},
		RunE: func(_ *cobra.Command, args []string) error {
			if cwd != "" {
				abs, err := filepath.Abs(cwd)
				if err != nil {
					return errfmt.Errorf("--cwd %s: %w", cwd, err)
				}
				cwd = abs
			}
			params := map[string]any{"name": name, "cwd": cwd, "ratio": ratio}
			// A side is sent as side, an axis as direction. An older daemon
			// that knows only the axes refuses a side for want of a
			// direction, so it never splits the wrong way.
			switch args[0] {
			case "left", "right", "up", "down":
				params["side"] = args[0]
			default:
				params["direction"] = args[0]
			}
			if len(args) > 1 {
				params["command"] = args[1:]
			}
			if noFocus {
				params["focus"] = false
			}
			if keepOpen {
				params["keep_open"] = true
			}
			return runLayoutVerb("split-window", sessionName, window, params, jsonOutput, func(res map[string]any) string {
				label, _ := res["name"].(string)
				return label
			})
		},
	}
	f := cmd.Flags()
	f.StringVarP(&sessionName, "session", "s", "", "Target session (default: this pane's session, else the most recently active)")
	f.StringVarP(&window, "window", "w", "", "Pane to split by name or ID (default: this pane, else the focused one)")
	f.StringVar(&name, "name", "", "Name for the new pane")
	f.StringVar(&cwd, "cwd", "", "Directory to start in (default: your directory in a pane, else the focused pane's)")
	f.IntVar(&ratio, "ratio", 0, "The new pane's share of the space, in percent (default: half)")
	f.BoolVar(&noFocus, "no-focus", false, "Leave the focus where it is")
	f.BoolVar(&keepOpen, "keep-open", false, "Keep the pane after its program exits")
	f.BoolVar(&jsonOutput, "json", false, "Output result as JSON")
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
	return cmd
}

// newLayoutCommands are zoom-window, swap-window and resize-window.
func newLayoutCommands() []*cobra.Command {
	var cmds []*cobra.Command
	add := func(use, short, long, example string, args cobra.PositionalArgs, params func(c *cobra.Command, args []string) (map[string]any, error)) {
		var sessionName, window string
		var jsonOutput bool
		c := &cobra.Command{Use: use, Short: short, Example: example, Args: args,
			Long: long + "\n\nWithout -w, the command acts on the pane you run it in, or on the focused pane\noutside every pane. Works with no client attached."}
		c.RunE = func(c *cobra.Command, args []string) error {
			p, err := params(c, args)
			if err != nil {
				return err
			}
			return runLayoutVerb(c.Name(), sessionName, window, p, jsonOutput, layoutLine)
		}
		f := c.Flags()
		f.StringVarP(&sessionName, "session", "s", "", "Target session (default: this pane's session, else the most recently active)")
		f.StringVarP(&window, "window", "w", "", "Pane by name or ID (default: this pane, else the focused one)")
		f.BoolVar(&jsonOutput, "json", false, "Output result as JSON")
		_ = c.RegisterFlagCompletionFunc("session", completeSessionNames)
		cmds = append(cmds, c)
	}
	add("zoom-window", "Zoom a pane to fill its workspace, or end the zoom",
		"Zoom a pane so it fills its workspace, or end the zoom. Without --on or --off\nthe command toggles the zoom. Zooming a pane on the workspace you see\nfocuses it.",
		"  tuios zoom-window -w build --on\n  tuios zoom-window -w build --off", cobra.NoArgs,
		func(c *cobra.Command, _ []string) (map[string]any, error) {
			on, _ := c.Flags().GetBool("on")
			off, _ := c.Flags().GetBool("off")
			switch {
			case on && off:
				return nil, errfmt.Errorf("--on and --off ask for opposite things. Pass one")
			case on, off:
				return map[string]any{"zoom": on}, nil
			}
			return map[string]any{}, nil
		})
	cmds[0].Flags().Bool("on", false, "Zoom the pane")
	cmds[0].Flags().Bool("off", false, "End the zoom")
	add("swap-window <other>", "Swap two panes in the layout",
		"Swap two tiled panes of one workspace. Each pane takes the place of the\nother. The focus stays where it is.",
		"  tuios swap-window -w build logs", cobra.ExactArgs(1),
		func(_ *cobra.Command, args []string) (map[string]any, error) {
			return map[string]any{"with": args[0]}, nil
		})
	add("resize-window <left|right|up|down> [percent]", "Move a border of a pane",
		"Move one border of a tiled pane by percent of the layout (1 to 50, default 5).\nright and down move a border right or down, left and up move it left or up.\nThe border is the one on that side of the pane, or the other one when the\npane is at the edge there.",
		"  tuios resize-window up 10 -w logs", cobra.RangeArgs(1, 2),
		func(_ *cobra.Command, args []string) (map[string]any, error) {
			p := map[string]any{"direction": args[0]}
			if len(args) > 1 {
				n, err := strconv.Atoi(args[1])
				if err != nil {
					return nil, errfmt.Errorf("percent must be a number from 1 to 50, got %q", args[1])
				}
				p["amount"] = n
			}
			return p, nil
		})
	return cmds
}

// layoutLine says what a zoom, swap or resize did.
func layoutLine(res map[string]any) string {
	if with, ok := res["with"].(string); ok {
		return "swapped with " + shortWindowID(with)
	}
	if z, ok := res["zoomed"].(bool); ok {
		return map[bool]string{true: "zoomed", false: "not zoomed"}[z]
	}
	if c, _ := res["changed"].(bool); c {
		return "resized"
	}
	return "no border could move"
}

// runLayoutVerb calls a layout verb on the pane -w names and prints the
// pane's short id and what line says about the result.
func runLayoutVerb(verb, sessionName, window string, params map[string]any, jsonOutput bool, line func(map[string]any) string) error {
	t, err := dialTarget(sessionName, window)
	if err != nil {
		return err
	}
	defer t.Close()
	params["window"] = window
	raw, err := t.client.Call(verb, t.params(params))
	if err != nil {
		return reportVerbError(t.explain(verb, err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResultOn(t, raw, jsonOutput)
	}
	var res map[string]any
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	id, _ := res["window_id"].(string)
	fmt.Printf("%s  %s%s\n", shortWindowID(id), line(res), t.on())
	return nil
}
