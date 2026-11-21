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

func runListVerbs(verb string, jsonOutput bool) error {
	var params any
	if verb != "" {
		params = map[string]any{"verb": verb}
	}
	raw, err := localVerb("list-verbs", params, 0, false)
	if err != nil {
		return err
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

	if verb == "" && len(catalog.ErrorCodes) > 0 {
		fmt.Println("Error codes:")
		for _, e := range catalog.ErrorCodes {
			fmt.Printf("  %-18s %s\n", e.Code, e.Description)
		}
	}
	return nil
}

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

func reportVerbError(err error, jsonOutput bool) error {
	if jsonOutput {
		outputJSON(verbErrorJSON(err))
		os.Exit(1)
	}
	return err
}

func verbErrorJSON(err error) map[string]any {
	out := map[string]any{"success": false, "error": err.Error()}
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

type newWindowOptions struct {
	session, name    string
	workspace        int
	cwd              string
	focus            bool
	command          []string
	host             string
	grants           []string
	kind, uri        string
	jsonOut          bool
	printID          bool
	keepOpen         bool
	wait             bool
	timeout          int
	shellIntegration string
}

var errStaleNewWindow = errors.New("the running daemon is older than this tuios and does not know --keep-open or --wait. Run 'tuios restart-server' to update it")

type captureOptions struct {
	session, window            string
	scrollback, ansi, resolved bool
	palette                    []string
	lines                      int
	lastCommand, unwrap, trim  bool
	jsonOut                    bool
	capped                     bool
	commands                   int
	explicitCommands           bool
}

const (
	captureScrollbackCap   = 2000
	captureDefaultCommands = 3
)

type windowRow struct {
	WindowID   string `json:"window_id"`
	Index      int    `json:"index"`
	Title      string `json:"title"`
	Display    string `json:"display_name"`
	CustomName string `json:"custom_name"`
	Workspace  int    `json:"workspace"`
	Focused    bool   `json:"focused"`
	Minimized  bool   `json:"minimized"`
	Scratch    bool   `json:"scratch"`
	AgentState string `json:"agent_state"`
	AgentMsg   string `json:"agent_message"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
}

type optionRow struct {
	Path        string   `json:"path"`
	Type        string   `json:"type"`
	Section     string   `json:"section"`
	Description string   `json:"description"`
	Default     string   `json:"default"`
	Accepted    []string `json:"accepted"`
	Min         int      `json:"min"`
	Max         int      `json:"max"`
	Unit        string   `json:"unit"`
	Auto        bool     `json:"auto"`
	Deprecated  string   `json:"deprecated"`
	SessionVal  string   `json:"session_value"`
}

type setAgentStateExtras struct {
	kind           string
	sessionID      string
	transcriptPath string
	ifState        string
}

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

type detectGroupMember struct {
	PID     int      `json:"pid"`
	Depth   int      `json:"depth"`
	Comm    string   `json:"comm"`
	Argv    []string `json:"argv"`
	Exe     string   `json:"exe"`
	Matched bool     `json:"matched"`
}

type screenExplanation struct {
	WindowID        string               `json:"window_id"`
	HarnessID       string               `json:"harness_id"`
	State           string               `json:"state"`
	Source          string               `json:"source"`
	Enabled         bool                 `json:"enabled"`
	Lines           int                  `json:"lines"`
	Tail            []string             `json:"tail"`
	Matched         bool                 `json:"matched"`
	Rule            int                  `json:"rule"`
	RuleState       string               `json:"rule_state"`
	Rules           []harness.RuleReport `json:"rules"`
	ManifestSource  string               `json:"manifest_source"`
	ReplacesBundled bool                 `json:"replaces_bundled"`
	Title           string               `json:"title"`
	Progress        string               `json:"progress"`
	TitleMatched    bool                 `json:"title_matched"`
	TitleRule       int                  `json:"title_rule"`
	TitleRuleState  string               `json:"title_rule_state"`
	TitleRules      []harness.RuleReport `json:"title_rules"`
}

type resultRenderer func(map[string]any) error

func sendAndWaitForResultWithFormat(client *session.Client, msg *session.Message, requestID string, jsonOutput bool, render resultRenderer) error {
	resp, err := client.SendControlMessage(msg)
	if err != nil {
		if jsonOutput {
			outputJSON(map[string]any{
				"success": false,
				"error":   fmt.Sprintf("failed to send command: %v", err),
			})
			return nil
		}
		return errfmt.Errorf("failed to send command: %w", err)
	}

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

func outputJSON(v any) {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return
	}
	_, _ = fmt.Fprintln(os.Stdout, jsonSafe(string(out)))
}

type runCommandEntry struct {
	name        string
	description string
	example     string
}

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

func runScreenshot(req screenshotRequest) error {
	raw, err := localVerb("screenshot", map[string]any{
		"session":    req.session,
		"window":     req.window,
		"format":     req.format,
		"theme":      req.theme,
		"frame":      req.frame,
		"scrollback": req.scrollback,
		"lines":      req.lines,
		"cursor":     req.cursor,
		"out":        req.out,
	}, 0, req.jsonOutput)
	if err != nil {
		return err
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

const defaultPopupCallTimeout = 30 * time.Second

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
			code = 130
		}
		return &statusError{code: code}
	}
	return nil
}

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
	wait      bool
	capture   bool
	timeout   int
}

func runPopup(o popupOptions) error {
	if o.dim != nil && (o.dim.Percent < 0 || o.dim.Percent > config.PopupDimMax) {
		return errfmt.Errorf("--dim must be an integer from 0 to 90")
	}
	if len(o.command) == 0 {
		return errfmt.Errorf("popup needs a command to run, e.g. tuios popup -- fzf")
	}

	params := map[string]any{
		"session": o.session,
		"command": o.command,
	}
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
		callTimeout = 24 * time.Hour
		if o.timeout > 0 {
			callTimeout = time.Duration(o.timeout)*time.Millisecond + 10*time.Second
		}
	}
	raw, err := localVerb("popup", params, callTimeout, o.jsonOut)
	if err != nil {
		return err
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
