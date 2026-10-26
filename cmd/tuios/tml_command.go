package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/spf13/cobra"
	"tuios.dev/tml"
)

// tml show, close and validate: TML surfaces from the command line. The
// daemon holds a surface and every attached client draws it; see
// internal/session/tml_surface.go.

// tmlShowOptions is the `tuios tml show` command line.
type tmlShowOptions struct {
	session   string
	pane      bool
	popup     bool
	name      string
	width     string
	height    string
	workspace int
	wait      bool
	timeout   int
	jsonOut   bool
	events    bool
}

func newTMLCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tml",
		Short: "Show TML documents: dashboards, results and forms",
		Long: `Show a TML document in tuios, as a popup or as a pane.

TML is a markup for terminal surfaces: a tree of known components, such as
text, tables, stats, charts, tabs and buttons, with a JSON state. tuios draws
it with your theme's colours. Keys and the mouse work in it. The daemon keeps
the surface, so it stays when you detach.

A document is TML text (an XML subset) or TML JSON. Run tuios tml validate to
check one, and tuios tml render to see it as text before you show it.`,
	}
	cmd.AddCommand(newTMLShowCommand(), newTMLCloseCommand(), newTMLValidateCommand(), newTMLRenderCommand(),
		newTMLPatchCommand(), newTMLReplaceCommand(), newTMLGetCommand(), newTMLListCommand())
	return cmd
}

func newTMLShowCommand() *cobra.Command {
	var o tmlShowOptions
	cmd := &cobra.Command{
		Use:   "show FILE",
		Short: "Show a TML document as a popup or a pane",
		Long: `Show a TML document as a popup or a pane, and print the surface id.

FILE is a TML text or TML JSON file, or - for standard input. tuios checks the
document first, and prints each problem with the fix.

A popup floats over the panes and needs an attached client. Press esc to close
it, unless the document uses esc. A pane is tiled like any pane.

With --wait the command stays open until the surface closes. It prints the
value the document closed with as JSON, such as the button a person pressed.
It prints null and exits with status 1 when the person closed the surface
without a value, and exits with status 2 when a program closed it with
tuios tml close: the value is then not the person's answer.

With --events the command prints the surface id, then one JSON line for each
event: an emit action in the document, such as emit('rerun'), and the close,
which is the last line. A script reads the lines and answers with tuios tml
patch or tuios tml replace. That makes the script the surface's owner.`,
		Example: `  # Show a report as a popup
  tuios tml show report.tml

  # Show a dashboard as a pane, from standard input
  some-generator | tuios tml show - --pane

  # Ask a question and use the answer
  answer=$(tuios tml show confirm.tml --wait)

  # Print each event of a dashboard as a JSON line, until it closes
  tuios tml show dash.tml --events

  # Change a value on a surface that is open
  tuios tml patch tml-4f1a9c02 '[{"op":"replace","path":"/runs","value":1}]'`,
		Args: cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runTMLShow(args[0], o, os.Stdin, os.Stdout)
		},
	}
	cmd.Flags().StringVarP(&o.session, "session", "s", "", "Target session (default: this pane's session, else the most recently active)")
	cmd.Flags().BoolVar(&o.popup, "popup", false, "Show it as a popup (the default)")
	cmd.Flags().BoolVar(&o.pane, "pane", false, "Show it as a tiled pane")
	cmd.Flags().StringVar(&o.name, "name", "", "Name for the window (default: the document's title)")
	cmd.Flags().StringVar(&o.width, "width", "", "Popup width in cells or percent (default: 80%)")
	cmd.Flags().StringVar(&o.height, "height", "", "Popup height in cells or percent (default: 60%)")
	cmd.Flags().IntVar(&o.workspace, "workspace", 0, "Workspace to open it on (default: the current one)")
	cmd.Flags().BoolVar(&o.wait, "wait", false, "Stay open until the surface closes, and print its value")
	cmd.Flags().IntVar(&o.timeout, "timeout", 0, "With --wait: milliseconds to wait (default: as long as it is open)")
	cmd.Flags().BoolVar(&o.jsonOut, "json", false, "Output the result as JSON")
	cmd.Flags().BoolVar(&o.events, "events", false, "Print the surface's events as JSON lines until it closes")
	cmd.MarkFlagsMutuallyExclusive("popup", "pane")
	cmd.MarkFlagsMutuallyExclusive("wait", "events")
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
	return cmd
}

// readTMLSource reads a document from a file or, for -, from stdin, up to
// the person's document limit and one byte more, so an over-long document is
// refused with the limit named.
func readTMLSource(name string, stdin io.Reader) ([]byte, error) {
	limit := tml.PersonLimits().DocBytes
	r := stdin
	if name != "-" {
		f, err := os.Open(name)
		if err != nil {
			return nil, err
		}
		defer func() { _ = f.Close() }()
		r = f
	}
	data, err := io.ReadAll(io.LimitReader(r, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > limit {
		return nil, fmt.Errorf("the document is larger than %d bytes, the limit", limit)
	}
	return data, nil
}

// checkTMLSource validates a document and prints its problems to w. It
// returns an error when the document has errors.
func checkTMLSource(name string, src []byte, w io.Writer) error {
	lim := tml.PersonLimits()
	_, problems, err := tml.Check(src, &tml.ValidateOptions{Limits: &lim})
	for _, p := range problems {
		fmt.Fprintf(w, "%s:%s\n", name, p.String())
	}
	if err != nil {
		return fmt.Errorf("%s is not a valid TML document: %w", name, err)
	}
	return nil
}

func runTMLShow(name string, o tmlShowOptions, stdin io.Reader, stdout io.Writer) error {
	src, err := readTMLSource(name, stdin)
	if err != nil {
		return err
	}
	if err := checkTMLSource(name, src, os.Stderr); err != nil {
		return err
	}
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	params := map[string]any{"doc": string(src)}
	if o.session != "" {
		params["session"] = o.session
	}
	if o.pane {
		params["target"] = session.TMLTargetPane
	}
	if o.name != "" {
		params["name"] = o.name
	}
	if o.width != "" {
		params["width"] = o.width
	}
	if o.height != "" {
		params["height"] = o.height
	}
	if o.workspace != 0 {
		params["workspace"] = o.workspace
	}
	callTimeout := defaultPopupCallTimeout
	if o.events {
		params["events"] = true
	}
	if o.wait {
		params["wait"] = true
		callTimeout = 24 * time.Hour
		if o.timeout > 0 {
			params["timeout"] = o.timeout
			callTimeout = time.Duration(o.timeout)*time.Millisecond + 10*time.Second
		}
	}
	raw, err := client.CallWithTimeout("tml-open", params, callTimeout)
	if err != nil {
		return reportVerbError(explainVerbError("tml-open", err), o.jsonOut)
	}
	if o.events {
		return streamTMLEvents(client, raw, stdout)
	}
	if o.jsonOut {
		fmt.Fprintln(stdout, string(raw))
		return nil
	}
	var res struct {
		Surface  string          `json:"surface"`
		WindowID string          `json:"window_id"`
		Reason   string          `json:"reason"`
		Value    json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	if !o.wait {
		fmt.Fprintf(stdout, "%s  %s\n", res.Surface, shortWindowID(res.WindowID))
		return nil
	}
	value := res.Value
	if len(value) == 0 {
		value = json.RawMessage("null")
	}
	fmt.Fprintln(stdout, string(value))
	switch res.Reason {
	case session.TMLDismissed:
		return &statusError{code: 1}
	case session.TMLClosedByProgram:
		return &statusError{code: 2}
	}
	return nil
}

func newTMLCloseCommand() *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "close SURFACE [VALUE]",
		Short: "Close a TML surface",
		Long: `Close a TML surface and its window.

VALUE is JSON, such as "done" or {"ok": true}. A tuios tml show --wait that
waits on the surface prints it and exits with status 2, so a script can tell
it from the person's answer. Without VALUE it prints null.`,
		Example: `  tuios tml close tml-4f1a9c02
  tuios tml close tml-4f1a9c02 '"done"'`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(_ *cobra.Command, args []string) error {
			params := map[string]any{"surface": args[0]}
			if len(args) == 2 {
				if !json.Valid([]byte(args[1])) {
					return fmt.Errorf("VALUE is not JSON. Quote a string, such as '\"done\"'")
				}
				params["value"] = json.RawMessage(args[1])
			}
			client, err := dialVerb()
			if err != nil {
				return err
			}
			defer func() { _ = client.Close() }()
			raw, err := client.Call("tml-close", params)
			if err != nil {
				return reportVerbError(explainVerbError("tml-close", err), jsonOut)
			}
			if jsonOut {
				fmt.Println(string(raw))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, "Output the result as JSON")
	return cmd
}

func newTMLValidateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "validate FILE",
		Short: "Check a TML document",
		Long: `Check a TML document, and print each problem with its place and the fix.

FILE is a TML text or TML JSON file, or - for standard input. The command
exits with status 1 when the document has errors. Warnings alone pass. It
needs no daemon.`,
		Example: `  tuios tml validate report.tml`,
		Args:    cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			src, err := readTMLSource(args[0], os.Stdin)
			if err != nil {
				return err
			}
			if err := checkTMLSource(args[0], src, os.Stdout); err != nil {
				return &statusError{code: 1}
			}
			return nil
		},
	}
}

// streamTMLEvents prints the answer of a tml-open with events, then each
// event line, and returns after the close event.
func streamTMLEvents(client *session.VerbClient, raw json.RawMessage, stdout io.Writer) error {
	fmt.Fprintln(stdout, string(raw))
	for {
		line, err := client.ReadEventLine(0)
		if err != nil {
			return fmt.Errorf("the event stream ended: %w", err)
		}
		fmt.Fprintln(stdout, string(line))
		var ev struct {
			TML *session.TMLEvent `json:"tml"`
		}
		if json.Unmarshal(line, &ev) == nil && ev.TML != nil && ev.TML.Name == "close" {
			return nil
		}
	}
}

// tmlCall runs one verb and prints its result as JSON.
func tmlCall(verb string, params map[string]any) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	raw, err := client.Call(verb, params)
	if err != nil {
		return explainVerbError(verb, err)
	}
	fmt.Println(string(raw))
	return nil
}

// readTMLArg reads an argument that is the value itself, or - for stdin.
func readTMLArg(arg string) ([]byte, error) {
	if arg != "-" {
		return []byte(arg), nil
	}
	return readTMLSource("-", os.Stdin)
}

func newTMLPatchCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "patch SURFACE OPS",
		Short: "Change a TML surface's state with a JSON Patch",
		Long: `Change a TML surface's state with a JSON Patch (RFC 6902), and print the
new version.

OPS is the patch as JSON, or - for standard input. Only the parts of the
surface that read the changed paths are drawn again. A test operation that
does not match refuses the whole patch: read the state with tuios tml get and
send the patch again.`,
		Example: `  tuios tml patch tml-4f1a9c02 '[{"op":"replace","path":"/failed","value":0}]'`,
		Args:    cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			ops, err := readTMLArg(args[1])
			if err != nil {
				return err
			}
			if !json.Valid(ops) {
				return fmt.Errorf("OPS is not JSON. Give a JSON Patch, such as '[{\"op\":\"replace\",\"path\":\"/n\",\"value\":1}]'")
			}
			return tmlCall("tml-patch", map[string]any{"surface": args[0], "ops": json.RawMessage(ops)})
		},
	}
}

func newTMLReplaceCommand() *cobra.Command {
	var keep bool
	cmd := &cobra.Command{
		Use:   "replace SURFACE FILE",
		Short: "Swap a TML surface's document",
		Long: `Swap a TML surface's document for the one in FILE, or - for standard input.

The state comes from the new document, unless --keep-state keeps the state the
surface has now. A table keeps its cursor row and tabs keep the open tab for
the nodes whose ids stay.`,
		Example: `  tuios tml replace tml-4f1a9c02 done.tml`,
		Args:    cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			src, err := readTMLSource(args[1], os.Stdin)
			if err != nil {
				return err
			}
			if err := checkTMLSource(args[1], src, os.Stderr); err != nil {
				return err
			}
			return tmlCall("tml-replace", map[string]any{"surface": args[0], "doc": string(src), "keep_state": keep})
		},
	}
	cmd.Flags().BoolVar(&keep, "keep-state", false, "Keep the surface's state instead of the new document's")
	return cmd
}

func newTMLGetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "get SURFACE",
		Short: "Print a TML surface: its document, state and version",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return tmlCall("tml-get", map[string]any{"surface": args[0]})
		},
	}
}

func newTMLListCommand() *cobra.Command {
	var sess string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the open TML surfaces of a session",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			params := map[string]any{}
			if sess != "" {
				params["session"] = sess
			}
			return tmlCall("tml-list", params)
		},
	}
	cmd.Flags().StringVarP(&sess, "session", "s", "", "Target session (default: this pane's session, else the most recently active)")
	_ = cmd.RegisterFlagCompletionFunc("session", completeSessionNames)
	return cmd
}
