package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/capture"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/spf13/cobra"
	"tuios.dev/shot"
)

// runListVerbs prints the control protocol's verb catalog. It is the discovery
// entry point the daemon's own error hints point at, so it must work whenever
// the daemon does, and say why when it does not.
func runListVerbs(verb string, jsonOutput bool) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	var params any
	if verb != "" {
		params = map[string]any{"verb": verb}
	}
	raw, err := client.Call("list-verbs", params)
	if err != nil {
		return explainVerbError("list-verbs", err)
	}

	if jsonOutput {
		var pretty any
		if err := jsonx.Unmarshal(raw, &pretty); err != nil {
			return errfmt.Errorf("failed to parse response: %w", err)
		}
		outputJSON(pretty)
		return nil
	}

	var catalog struct {
		Version       int    `json:"version"`
		MinVersion    int    `json:"min_version"`
		DaemonVersion string `json:"daemon_version"`
		Verbs         []struct {
			Verb        string `json:"verb"`
			Description string `json:"description"`
			Params      []struct {
				Name        string   `json:"name"`
				Type        string   `json:"type"`
				Required    bool     `json:"required"`
				Description string   `json:"description"`
				Accepted    []string `json:"accepted"`
				Default     string   `json:"default"`
			} `json:"params"`
			Examples []string `json:"examples"`
		} `json:"verbs"`
		ErrorCodes []struct {
			Code        string `json:"code"`
			Description string `json:"description"`
		} `json:"error_codes"`
	}
	if err := jsonx.Unmarshal(raw, &catalog); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}

	fmt.Printf("Control protocol version %d (daemon %s, oldest supported %d)\n\n",
		catalog.Version, catalog.DaemonVersion, catalog.MinVersion)

	for _, v := range catalog.Verbs {
		fmt.Printf("%s\n  %s\n", v.Verb, v.Description)
		for _, p := range v.Params {
			required := ""
			if p.Required {
				required = " (required)"
			}
			fmt.Printf("    %-10s %-8s%s %s\n", p.Name, p.Type, required, p.Description)
			if len(p.Accepted) > 0 {
				fmt.Printf("    %-10s %s\n", "", "one of: "+strings.Join(p.Accepted, ", "))
			}
			if p.Default != "" {
				fmt.Printf("    %-10s %s\n", "", "default: "+p.Default)
			}
		}
		for _, ex := range v.Examples {
			fmt.Printf("    example: %s\n", ex)
		}
		fmt.Println()
	}

	// The error vocabulary only makes sense alongside the whole catalog, so it
	// is omitted when a single verb was requested.
	if verb == "" && len(catalog.ErrorCodes) > 0 {
		fmt.Println("Error codes:")
		for _, e := range catalog.ErrorCodes {
			fmt.Printf("  %-18s %s\n", e.Code, e.Description)
		}
	}
	return nil
}

// printVerbResult renders a verb result in the CLI's --json contract shape
// (success/message plus the result fields), or a short human line otherwise.
func printVerbResult(raw json.RawMessage, jsonOutput bool) error {
	if !jsonOutput {
		fmt.Println("Command executed successfully")
		return nil
	}
	var fields map[string]any
	if err := jsonx.Unmarshal(raw, &fields); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	out := map[string]any{"success": true, "message": "command executed"}
	for k, v := range fields {
		if k == "type" {
			continue
		}
		out[k] = v
	}
	outputJSON(out)
	return nil
}

// reportVerbError renders a failed verb call, honoring --json by printing a
// {success:false,error} object and exiting non-zero.
//
// An error another machine answered carries host and untrusted, as a result
// from there does.
func reportVerbError(err error, jsonOutput bool) error {
	if jsonOutput {
		outputJSON(verbErrorJSON(err))
		os.Exit(1)
	}
	return err
}

// verbErrorJSON is the object reportVerbError prints for err under --json.
func verbErrorJSON(err error) map[string]any {
	out := map[string]any{"success": false, "error": err.Error()}
	// The agents-off answer carries the daemon's code, so a program can tell
	// it from a failure worth retrying.
	if call, ok := errors.AsType[*session.VerbCallError](err); ok && call.Code == session.ErrVerbAgentsDisabled {
		out["error"] = config.AgentsOffMessage
		out["code"] = call.Code
	}
	if here, ok := errors.AsType[*session.AgentsOffHereError](err); ok {
		out["error"] = here.Error()
		out["code"] = here.ErrorCode()
	}
	if h, ok := errors.AsType[*hostError](err); ok {
		markUntrusted(out, h.host)
	}
	return out
}

// newWindowOptions is the `tuios new-window` command line. workspace 0 means
// the current one, an empty cwd means the daemon's own directory, and a
// non-empty command is an argv the window execs as its process instead of a
// shell. A non-empty host puts the window's process on another machine; the
// window is still this session's. Non-empty grants are what the window's
// process may do through tuios.
type newWindowOptions struct {
	session, name string
	workspace     int
	cwd           string
	focus         bool
	command       []string
	host          string
	grants        []string
	kind, uri     string
	jsonOut       bool
	printID       bool
	// keepOpen keeps the window after its program exits. wait waits for the
	// exit and exits with the program's status; timeout bounds that wait in
	// milliseconds, 0 for none.
	keepOpen bool
	wait     bool
	timeout  int
	// shellIntegration is auto or off for the pane's shell, "" for the
	// config's [shell] integration.
	shellIntegration string
}

// errStaleNewWindow is what new-window says when the daemon does not know
// keep_open or wait. Since v0.8.0 a daemon refuses a parameter it does not
// know, and its own wording ("fix the name") points at the caller instead of
// at the daemon. An older one ignores the parameter and answers without the
// fields that say it was used.
var errStaleNewWindow = errors.New("the running daemon is older than this tuios and does not know --keep-open or --wait. Run 'tuios restart-server' to update it")

// runCapturePane captures the content of a pane and prints to stdout. lines
// keeps only the last N lines when positive, which is what bounds a capture of a
// long scrollback to something a caller can actually read.
//
// A capture from another machine is fenced as untrusted content, like mail,
// because it is text a program there wrote. Control characters are removed
// from it unless the caller asked for escape codes with --ansi or --resolved.
// captureOptions is the `tuios capture-pane` command line.
type captureOptions struct {
	session, window            string
	scrollback, ansi, resolved bool
	palette                    []string
	lines                      int
	lastCommand, unwrap, trim  bool
	jsonOut                    bool
	// capped is true when lines (and commands) are the default cap rather
	// than the caller's own --lines.
	capped bool
	// commands asks for the last N shell commands, under lines in total.
	commands         int
	explicitCommands bool
}

// captureScrollbackCap is the most lines capture-pane --scrollback keeps when
// no --lines is given, and captureDefaultCommands how many commands it keeps.
// A full history is 10,000 lines or more, which floods an agent's context.
// The cap lives here and not in the verb: the tmux shim, the MCP server and
// the web client call capture-pane for the whole history.
const (
	captureScrollbackCap   = 2000
	captureDefaultCommands = 3
)

// errStaleCommands is what capture-pane --commands says when the daemon does
// not know the commands parameter, worded like errStaleCapture.
var errStaleCommands = errors.New("the running daemon is older than this tuios and does not know --commands. Run 'tuios restart-server' to update it")

// errStaleCapture is what capture-pane says when the daemon does not know
// unwrap or trim. Since v0.8.0 a daemon refuses a parameter it does not know,
// and its own wording ("fix the name") points at the caller instead of at the
// daemon.
var errStaleCapture = errors.New("the running daemon is older than this tuios and does not know --unwrap or --trim. Run 'tuios restart-server' to update it")

// errStaleWaitNew is what wait-for --new says when the daemon does not know
// the new parameter, worded like errStaleCapture.
var errStaleWaitNew = errors.New("the running daemon is older than this tuios and does not know --new. Run 'tuios restart-server' to update it")

// windowRow is the subset of a listed window both the table and the single
// window view render.
type windowRow struct {
	WindowID   string `json:"window_id"`
	Index      int    `json:"index"`
	Title      string `json:"title"`
	Display    string `json:"display_name"`
	CustomName string `json:"custom_name"`
	Workspace  int    `json:"workspace"`
	Focused    bool   `json:"focused"`
	Minimized  bool   `json:"minimized"`
	// Scratch marks a pane of a scratch group. The table leaves it out, as
	// the dock and the rail do. --json keeps it, marked.
	Scratch    bool   `json:"scratch"`
	AgentState string `json:"agent_state"`
	AgentMsg   string `json:"agent_message"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
}

// optionRow is one settable configuration path as list-options reports it.
type optionRow struct {
	Path        string   `json:"path"`
	Type        string   `json:"type"`
	Section     string   `json:"section"`
	Description string   `json:"description"`
	Default     string   `json:"default"`
	Accepted    []string `json:"accepted"`
	Min         int      `json:"min"`
	Max         int      `json:"max"`
	Auto        bool     `json:"auto"`
	Deprecated  string   `json:"deprecated"`
	SessionVal  string   `json:"session_value"`
}

// setAgentStateExtras are the set-agent-state fields a hook reporter adds.
type setAgentStateExtras struct {
	kind           string
	sessionID      string
	transcriptPath string
	ifState        string
}

// callAndReport makes a one-shot verb call and hands the decoded result to a
// printer, so the small setter commands do not each repeat the dial, the error
// wrapping, and the decode.
func callAndReport(verb string, params map[string]any, report func(map[string]any)) error {
	sessionName, _ := params["session"].(string)
	t, err := dialSessionTarget(sessionName)
	if err != nil {
		return err
	}
	defer t.Close()

	raw, err := t.client.Call(verb, t.params(params))
	if err != nil {
		return t.explain(verb, err)
	}
	var res map[string]any
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	report(res)
	return nil
}

// detectExplanation is the explain-agent-detect result, decoded for printing.
type detectExplanation struct {
	WindowID       string                 `json:"window_id"`
	State          string                 `json:"state"`
	Source         string                 `json:"source"`
	HarnessID      string                 `json:"harness_id"`
	AutoDetected   bool                   `json:"auto_detected"`
	Identity       string                 `json:"identity"`
	Confidence     string                 `json:"confidence"`
	NeedsYou       bool                   `json:"needs_you"`
	Activity       string                 `json:"activity"`
	Running        bool                   `json:"running"`
	Reason         string                 `json:"reason"`
	Verdict        string                 `json:"verdict"`
	Evidence       []string               `json:"evidence"`
	Ignored        []string               `json:"ignored"`
	Process        harness.ProcReport     `json:"process"`
	Manifests      []harness.DetectReport `json:"manifests"`
	Group          []detectGroupMember    `json:"group"`
	NameList       string                 `json:"name_list"`
	Matched        bool                   `json:"matched"`
	MatchedHarness string                 `json:"matched_harness"`
	MatchedRule    string                 `json:"matched_rule"`
	MatchedVia     []string               `json:"matched_via"`
	Note           string                 `json:"note"`
}

// detectGroupMember is one process the detector read behind a wrapper.
type detectGroupMember struct {
	PID     int      `json:"pid"`
	Depth   int      `json:"depth"`
	Comm    string   `json:"comm"`
	Argv    []string `json:"argv"`
	Exe     string   `json:"exe"`
	Matched bool     `json:"matched"`
}

// screenExplanation is the explain-agent-screen result, decoded for printing.
type screenExplanation struct {
	WindowID  string               `json:"window_id"`
	HarnessID string               `json:"harness_id"`
	State     string               `json:"state"`
	Source    string               `json:"source"`
	Enabled   bool                 `json:"enabled"`
	Lines     int                  `json:"lines"`
	Tail      []string             `json:"tail"`
	Matched   bool                 `json:"matched"`
	Rule      int                  `json:"rule"`
	RuleState string               `json:"rule_state"`
	Rules     []harness.RuleReport `json:"rules"`
	// ManifestSource is "bundled" or the user file in force, and
	// ReplacesBundled says that file took a bundled manifest's place.
	ManifestSource  string `json:"manifest_source"`
	ReplacesBundled bool   `json:"replaces_bundled"`
	// The title block: the pane's title, its last progress report, and what
	// each title rule made of them.
	Title          string               `json:"title"`
	Progress       string               `json:"progress"`
	TitleMatched   bool                 `json:"title_matched"`
	TitleRule      int                  `json:"title_rule"`
	TitleRuleState string               `json:"title_rule_state"`
	TitleRules     []harness.RuleReport `json:"title_rules"`
}

// resultRenderer prints a command result's data for a human reader.
type resultRenderer func(map[string]any) error

// sendAndWaitForResultWithFormat sends a message and waits for the result.
// If jsonOutput is true, outputs machine-readable JSON.
func sendAndWaitForResultWithFormat(client *session.Client, msg *session.Message, requestID string, jsonOutput bool, render resultRenderer) error {
	resp, err := client.SendControlMessage(msg)
	if err != nil {
		if jsonOutput {
			outputJSON(map[string]any{
				"success": false,
				"error":   fmt.Sprintf("failed to send command: %v", err),
			})
			return nil // Don't return error, we already output JSON
		}
		return errfmt.Errorf("failed to send command: %w", err)
	}

	// Check response type
	switch resp.Type {
	case session.MsgCommandResult:
		var result session.CommandResultPayload
		if err := resp.ParsePayload(&result); err != nil {
			if jsonOutput {
				outputJSON(map[string]any{
					"success": false,
					"error":   fmt.Sprintf("failed to parse response: %v", err),
				})
				return nil
			}
			return errfmt.Errorf("failed to parse response: %w", err)
		}
		if jsonOutput {
			output := map[string]any{
				"success": result.Success,
				"message": result.Message,
			}
			// Merge any additional data from the result
			maps.Copy(output, result.Data)
			outputJSON(output)
			if !result.Success {
				os.Exit(1)
			}
			return nil
		}
		if !result.Success {
			return errfmt.Errorf("command failed: %s", result.Message)
		}
		if render != nil {
			return render(result.Data)
		}
		fmt.Printf("Command executed successfully: %s\n", result.Message)
		return nil

	case session.MsgError:
		var errPayload session.ErrorPayload
		if err := resp.ParsePayload(&errPayload); err != nil {
			if jsonOutput {
				outputJSON(map[string]any{
					"success": false,
					"error":   "command failed with unknown error",
				})
				return nil
			}
			return errfmt.Errorf("the command failed, and the daemon did not say why. Run 'tuios logs' to read what it logged")
		}
		if jsonOutput {
			outputJSON(map[string]any{
				"success": false,
				"error":   errPayload.Message,
			})
			os.Exit(1)
			return nil
		}
		return errfmt.Errorf("command failed: %s", errPayload.Message)

	default:
		// Command was sent, we got some response
		if jsonOutput {
			outputJSON(map[string]any{
				"success":    true,
				"request_id": requestID[:8],
			})
			return nil
		}
		fmt.Printf("Command sent (request ID: %s)\n", requestID[:8])
		return nil
	}
}

// outputJSON outputs a value as JSON to stdout.
func outputJSON(v any) {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return
	}
	// jsonSafe escapes the C1 controls encoding/json leaves raw, so a name
	// from a far host cannot drive the terminal the JSON is shown on.
	_, _ = fmt.Fprintln(os.Stdout, jsonSafe(string(out)))
}

// runCommandEntry is one row of 'tuios run-command --list'.
type runCommandEntry struct {
	name        string
	description string
	example     string
}

// Completion functions for shell autocompletion

// screenshotRequest is the CLI's resolved screenshot call.
type screenshotRequest struct {
	session    string
	window     string
	format     string
	theme      string
	frame      string
	out        string
	lines      int
	scrollback bool
	cursor     bool
	copy       bool
	noCopy     bool
	jsonOutput bool
}

// runScreenshot renders a window to an image file through the daemon and then
// says where the file is.
//
// The daemon writes the file, not the CLI. They talk over a unix socket, so
// they are the same machine and the path the daemon reports is a path the
// caller can open. That is also why a clipboard copy is attempted here: this
// process is on the user's own machine, which is the rule an image copy has to
// obey.
func runScreenshot(req screenshotRequest) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	raw, err := client.Call("screenshot", map[string]any{
		"session":    req.session,
		"window":     req.window,
		"format":     req.format,
		"theme":      req.theme,
		"frame":      req.frame,
		"scrollback": req.scrollback,
		"lines":      req.lines,
		"cursor":     req.cursor,
		"out":        req.out,
	})
	if err != nil {
		return reportVerbError(explainVerbError("screenshot", err), req.jsonOutput)
	}

	var res struct {
		Path     string   `json:"path"`
		Host     string   `json:"host"`
		Format   string   `json:"format"`
		Cols     int      `json:"cols"`
		Rows     int      `json:"rows"`
		Bytes    int      `json:"bytes"`
		Warnings []string `json:"warnings"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}

	copied, copyNote := maybeCopyScreenshot(res.Path, res.Format, req)

	if req.jsonOutput {
		out := map[string]any{
			"success": true, "path": res.Path, "host": res.Host,
			"format": res.Format, "cols": res.Cols, "rows": res.Rows,
			"bytes": res.Bytes, "copied": copied,
			"warnings": append(append([]string{}, res.Warnings...), copyNote...),
		}
		outputJSON(out)
		return nil
	}
	fmt.Println(res.Path)
	for _, w := range res.Warnings {
		fmt.Fprintln(os.Stderr, w)
	}
	for _, w := range copyNote {
		fmt.Fprintln(os.Stderr, w)
	}
	return nil
}

// maybeCopyScreenshot attempts the clipboard copy the flags asked for and
// reports, in plain words, what actually happened.
func maybeCopyScreenshot(path, format string, req screenshotRequest) (bool, []string) {
	if req.noCopy || !req.copy || path == "" {
		return false, nil
	}
	f, ok := shot.ParseFormat(format)
	if !ok {
		return false, nil
	}
	data, err := os.ReadFile(path) // #nosec G304 - the daemon just wrote this path
	if err != nil {
		return false, []string{"The file was written but could not be read back to copy it."}
	}
	if _, err := capture.CopyImage(path, data, f.MediaType()); err != nil {
		return false, []string{"The file was saved. " + err.Error()}
	}
	return true, nil
}

// defaultPopupCallTimeout is the read deadline of a popup call that does not
// wait, the verb client's own default.
const defaultPopupCallTimeout = 30 * time.Second

// printPopupResult prints a waited popup's captured output as it was printed,
// and returns the command's status as this process's.
func printPopupResult(w io.Writer, raw json.RawMessage) error {
	var res struct {
		ExitCode int    `json:"exit_code"`
		Stdout   string `json:"stdout"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	fmt.Fprint(w, res.Stdout)
	if res.ExitCode != 0 {
		code := res.ExitCode
		if code < 0 {
			// Ended by a signal, which is how closing the popup ends it.
			code = 130
		}
		return &statusError{code: code}
	}
	return nil
}

// popupOptions is the `tuios popup` command line, gathered so the runner reads
// as one thing rather than as seven positional arguments.
type popupOptions struct {
	dim       *config.PopupDim
	session   string
	name      string
	cwd       string
	width     string
	height    string
	workspace int
	command   []string
	jsonOut   bool
	// wait keeps the call open until the command exits; capture returns its
	// standard output; timeout bounds the wait in milliseconds, 0 for none.
	wait    bool
	capture bool
	timeout int
}

// runPopup opens a popup and reports its id, which is the handle every later
// call needs.
//
// The command is refused here as well as in the daemon so the message names the
// flag the user typed rather than the parameter the wire carries.
func runPopup(o popupOptions) error {
	if o.dim != nil && (o.dim.Percent < 0 || o.dim.Percent > config.PopupDimMax) {
		return errfmt.Errorf("--dim must be an integer from 0 to 90")
	}
	if len(o.command) == 0 {
		return errfmt.Errorf("popup needs a command to run, e.g. tuios popup -- fzf")
	}
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	params := map[string]any{
		"session": o.session,
		"command": o.command,
	}
	// Only the parameters the user named are sent. The daemon refuses a params
	// key a verb does not declare and fills its own defaults for the rest, so
	// sending an empty width would ask for a size nobody chose.
	if o.dim != nil {
		params["dim"] = o.dim
	}
	if o.width != "" {
		params["width"] = o.width
	}
	if o.height != "" {
		params["height"] = o.height
	}
	if o.name != "" {
		params["name"] = o.name
	}
	if o.cwd != "" {
		params["cwd"] = o.cwd
	}
	if o.workspace != 0 {
		params["workspace"] = o.workspace
	}
	callTimeout := defaultPopupCallTimeout
	if o.wait {
		params["wait"] = true
		if o.capture {
			params["capture_stdout"] = true
		}
		if o.timeout > 0 {
			params["timeout"] = o.timeout
		}
		// The daemon answers when the popup's command exits, which is when
		// the person is done with it. With no timeout there is no bound worth
		// guessing, so the call waits a day.
		callTimeout = 24 * time.Hour
		if o.timeout > 0 {
			callTimeout = time.Duration(o.timeout)*time.Millisecond + 10*time.Second
		}
	}
	raw, err := client.CallWithTimeout("popup", params, callTimeout)
	if err != nil {
		return reportVerbError(explainVerbError("popup", err), o.jsonOut)
	}
	if o.jsonOut {
		return printVerbResult(raw, o.jsonOut)
	}
	if o.wait {
		return printPopupResult(os.Stdout, raw)
	}
	var res struct {
		WindowID string `json:"window_id"`
		Name     string `json:"name"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	fmt.Printf("%s  %s\n", shortWindowID(res.WindowID), res.Name)
	return nil
}

// completeHostNames offers the machines in the [hosts] table, each with the
// state of its link, so a completion does not silently suggest a machine that
// is down. A daemon that is not running, or one with no hosts, offers nothing
// rather than an error: a completion is not the place to report a problem.
func completeHostNames(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	if !session.IsDaemonRunning() {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	client, err := dialVerb()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	defer func() { _ = client.Close() }()

	raw, err := client.Call("list-hosts", map[string]any{})
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var res struct {
		Hosts []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"hosts"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	names := make([]string, 0, len(res.Hosts))
	for _, h := range res.Hosts {
		names = append(names, fmt.Sprintf("%s\t%s", h.Name, h.Status))
	}
	return names, cobra.ShellCompDirectiveNoFileComp
}
