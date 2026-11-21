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

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/Gaurav-Gosain/tuios/internal/session"
)

func runSendKeys(sessionName, keys string, literal bool, raw bool, windowTarget string, repeat int, allowBlocked, jsonOutput bool) error {
	t, err := dialTarget(sessionName, windowTarget)
	if err != nil {
		return reportVerbError(err, jsonOutput)
	}
	defer t.Close()

	params := map[string]any{
		"session": sessionName,
		"window":  windowTarget,
		"keys":    keys,
		"literal": literal,
		"raw":     raw,
	}
	if repeat > 1 {
		params["repeat"] = repeat
	}
	if allowBlocked {
		params["allow_blocked"] = true
	}
	raw2, err := t.client.Call("send-keys", t.params(params))
	if err != nil {
		return reportVerbError(t.explain("send-keys", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResultOn(t, raw2, true)
	}
	var res struct {
		SentTo   string `json:"sent_to"`
		WindowID string `json:"window_id"`
		Window   string `json:"window"`
		Keys     int    `json:"keys"`
	}
	if err := jsonx.Unmarshal(raw2, &res); err != nil || res.SentTo == "" {
		return nil
	}
	fmt.Println(sendKeysSummary(res.SentTo, res.WindowID, res.Window, res.Keys))
	return nil
}

func sendKeysSummary(sentTo, windowID, window string, keys int) string {
	noun := "keys"
	if keys == 1 {
		noun = "key"
	}
	if sentTo == "client" {
		return fmt.Sprintf("sent %d %s to the attached client (the focused window, or the window manager)", keys, noun)
	}
	if window == "" {
		return fmt.Sprintf("sent %d %s to window %s", keys, noun, shortWindowID(windowID))
	}
	return fmt.Sprintf("sent %d %s to window %s (%s)", keys, noun, window, shortWindowID(windowID))
}

func runNewWindow(o newWindowOptions) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	params := map[string]any{
		"session":   o.session,
		"name":      o.name,
		"workspace": o.workspace,
		"cwd":       o.cwd,
		"focus":     o.focus,
	}
	if len(o.command) > 0 {
		params["command"] = o.command
	}
	if o.host != "" {
		params["host"] = o.host
	}
	if len(o.grants) > 0 {
		params["grants"] = o.grants
	}
	if o.kind != "" {
		params["kind"] = o.kind
	}
	if o.uri != "" {
		params["uri"] = o.uri
	}
	if o.keepOpen {
		params["keep_open"] = true
	}
	if o.shellIntegration != "" {
		params["shell_integration"] = o.shellIntegration
	}
	call := client.Call
	if o.wait {
		params["wait"] = true
		if o.timeout > 0 {
			params["timeout"] = o.timeout
		}
		callTimeout := 24 * time.Hour
		if o.timeout > 0 {
			callTimeout = time.Duration(o.timeout)*time.Millisecond + 10*time.Second
		}
		call = func(verb string, params any) (json.RawMessage, error) {
			return client.CallWithTimeout(verb, params, callTimeout)
		}
	}
	raw, err := call("new-window", params)
	if err != nil {
		if staleNewWindowParam(err) {
			err = errStaleNewWindow
		}
		if err != errStaleNewWindow {
			err = explainVerbError("new-window", err)
		}
		return reportVerbError(err, o.jsonOut)
	}
	var res struct {
		Type     string `json:"type"`
		WindowID string `json:"window_id"`
		Name     string `json:"name"`
		Host     string `json:"host"`
		KeepOpen bool   `json:"keep_open"`
		ExitCode *int   `json:"exit_code"`
		Signal   int    `json:"signal"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	if (o.keepOpen && !res.KeepOpen) || (o.wait && res.ExitCode == nil) {
		if o.wait {
			return reportVerbError(errStaleNewWindow, o.jsonOut)
		}
		fmt.Fprintln(os.Stderr, "warning: "+errStaleNewWindow.Error()+".")
	}
	if o.jsonOut {
		if err := printVerbResult(raw, true); err != nil {
			return err
		}
		return newWindowExitStatus(o.wait, res.ExitCode, res.Signal)
	}
	if o.wait {
		return newWindowExitStatus(true, res.ExitCode, res.Signal)
	}
	if o.printID {
		fmt.Println(res.WindowID)
		return nil
	}
	if res.Host != "" {
		fmt.Printf("%s  %s  on %s\n", shortWindowID(res.WindowID), res.Name, res.Host)
		return nil
	}
	fmt.Printf("%s  %s\n", shortWindowID(res.WindowID), res.Name)
	return nil
}

func staleNewWindowParam(err error) bool {
	call, ok := errors.AsType[*session.VerbCallError](err)
	return ok && call.Code == session.ErrVerbInvalidParams && call.Hint != nil &&
		(call.Hint.Param == "keep_open" || call.Hint.Param == "wait" || call.Hint.Param == "timeout")
}

func newWindowExitStatus(wait bool, code *int, signal int) error {
	if !wait || code == nil {
		return nil
	}
	switch {
	case signal > 0:
		return &statusError{code: 128 + signal}
	case *code < 0:
		return &statusError{code: 1}
	case *code > 0:
		return &statusError{code: *code}
	}
	return nil
}

func runFocusWindow(sessionName, windowTarget, relative, direction string, jsonOutput bool) error {
	t, err := dialTarget(sessionName, windowTarget)
	if err != nil {
		return err
	}
	defer t.Close()

	raw, err := t.client.Call("focus-window", t.params(map[string]any{
		"session":   sessionName,
		"window":    windowTarget,
		"relative":  relative,
		"direction": direction,
	}))
	if err != nil {
		return reportVerbError(t.explain("focus-window", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResultOn(t, raw, jsonOutput)
	}
	var res struct {
		FocusedWindowID string    `json:"focused_window_id"`
		Window          windowRow `json:"window"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	if res.FocusedWindowID == "" {
		fmt.Println("No window has the focus.")
		return nil
	}
	fmt.Printf("%s  %s%s\n", shortWindowID(res.FocusedWindowID), windowLabel(res.Window), t.on())
	return nil
}

func runMoveWindow(sessionName, windowTarget, toSession string, workspace int, follow, jsonOutput bool) error {

	params := map[string]any{
		"session": sessionName,
		"window":  windowTarget,
		"follow":  follow,
	}
	if workspace != 0 {
		params["workspace"] = workspace
	}
	if toSession != "" {
		params["to_session"] = toSession
	}
	raw, err := localVerb("move-window", params, 0, jsonOutput)
	if err != nil {
		return err
	}
	if jsonOutput {
		return printVerbResult(raw, jsonOutput)
	}
	var res struct {
		WindowID    string `json:"window_id"`
		From        int    `json:"from_workspace"`
		To          int    `json:"workspace"`
		Session     string `json:"session"`
		FromSession string `json:"from_session"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	if res.FromSession != "" {
		fmt.Printf("moved %s from session %s to session %s, workspace %d\n", shortWindowID(res.WindowID), res.FromSession, res.Session, res.To)
		return nil
	}
	fmt.Printf("moved %s from workspace %d to %d\n", shortWindowID(res.WindowID), res.From, res.To)
	return nil
}

func runSetWindow(sessionName, windowTarget string, name *string, minimized *bool, jsonOutput bool) error {

	params := map[string]any{
		"session": sessionName,
		"window":  windowTarget,
	}
	if name != nil {
		params["name"] = *name
	}
	if minimized != nil {
		params["minimized"] = *minimized
	}

	raw, err := localVerb("set-window", params, 0, jsonOutput)
	if err != nil {
		return err
	}
	if jsonOutput {
		return printVerbResult(raw, jsonOutput)
	}
	var res windowRow
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	state := "restored"
	if res.Minimized {
		state = "minimized"
	}
	fmt.Printf("%s  %s  %s\n", shortWindowID(res.WindowID), windowLabel(res), state)
	return nil
}

func runSendText(sessionName, windowTarget, text string, allowBlocked, jsonOutput bool) error {
	t, err := dialTarget(sessionName, windowTarget)
	if err != nil {
		return err
	}
	defer t.Close()

	params := map[string]any{
		"session": sessionName,
		"window":  windowTarget,
		"text":    text,
	}
	if allowBlocked {
		params["allow_blocked"] = true
	}
	raw, err := t.client.Call("send-text", t.params(params))
	if err != nil {
		return t.explain("send-text", err)
	}
	if jsonOutput {
		var pretty any
		if err := jsonx.Unmarshal(raw, &pretty); err != nil {
			return errfmt.Errorf("failed to parse response: %w", err)
		}
		return printJSON(pretty)
	}
	return nil
}

func runCapturePane(o captureOptions) error {
	t, err := dialTarget(o.session, o.window)
	if err != nil {
		return err
	}
	defer t.Close()

	params := map[string]any{
		"session":    o.session,
		"window":     o.window,
		"scrollback": o.scrollback,
		"ansi":       o.ansi,
		"resolved":   o.resolved,
		"palette":    o.palette,
		"lines":      o.lines,
	}
	if o.lastCommand {
		params["source"] = "last-command-output"
	}
	if o.unwrap {
		params["unwrap"] = true
	}
	if o.trim {
		params["trim"] = true
	}
	if o.commands > 0 {
		params["commands"] = o.commands
	}
	raw, err := t.client.Call("capture-pane", t.params(params))
	if err != nil {
		return reportVerbError(t.explain("capture-pane", err), o.jsonOut)
	}
	if (o.capped || o.explicitCommands) && !o.jsonOut {
		if note := captureCapNote(raw, o.explicitCommands); note != "" {
			fmt.Fprintln(os.Stderr, note)
		}
	}
	if o.jsonOut {
		return printVerbResultOn(t, raw, o.jsonOut)
	}
	return printCapture(os.Stdout, raw, t.host, t.window, o.ansi || o.resolved)
}

func captureCapNote(raw json.RawMessage, explicit bool) string {
	var res struct {
		Content           string            `json:"content"`
		LinesOmitted      *int              `json:"lines_omitted"`
		CommandMarks      *bool             `json:"command_marks"`
		Commands          []json.RawMessage `json:"commands"`
		CommandsAvailable int               `json:"commands_available"`
	}
	if jsonx.Unmarshal(raw, &res) != nil {
		return ""
	}
	const tail = " To get the whole history, use --lines 0."
	omitted := 0
	if res.LinesOmitted != nil {
		omitted = *res.LinesOmitted
	}
	full := strings.Count(strings.TrimSuffix(res.Content, "\n"), "\n")+1 >= captureScrollbackCap
	switch {
	case res.CommandMarks != nil && *res.CommandMarks:
		shown := len(res.Commands)
		if res.CommandsAvailable <= shown && omitted == 0 {
			return ""
		}
		note := fmt.Sprintf("note: capture-pane shows the last %d commands", shown)
		if res.CommandsAvailable > shown {
			note += fmt.Sprintf(" of %s", commas(res.CommandsAvailable))
		}
		note += "."
		if omitted > 0 {
			note += fmt.Sprintf(" It left out %s lines from the middle of their output.", commas(omitted))
		}
		return note + tail
	case res.CommandMarks != nil:
		if omitted == 0 && !explicit {
			return ""
		}
		note := fmt.Sprintf("note: the shell in this pane sends no command marks, so capture-pane shows the last %s lines.", commas(captureScrollbackCap))
		if omitted > 0 {
			note += fmt.Sprintf(" It left out %s older lines.", commas(omitted))
		}
		return note + " Run 'tuios doctor shell' to turn the marks on." + tail
	case omitted > 0:
		return fmt.Sprintf("note: capture-pane shows the last %s lines. It left out %s older lines.", commas(captureScrollbackCap), commas(omitted)) + tail
	case res.LinesOmitted == nil && full:
		return fmt.Sprintf("note: capture-pane shows the last %s lines. Older lines can be left out.", commas(captureScrollbackCap)) + tail
	}
	return ""
}

func printCapture(w io.Writer, raw json.RawMessage, host, window string, keepEscapes bool) error {
	var res struct {
		Content string `json:"content"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	if host == "" {
		fmt.Fprint(w, res.Content)
		return nil
	}
	who := "the focused pane on " + plainLine(host)
	if window != "" {
		who = "pane " + plainLine(window) + " on " + plainLine(host)
	}
	content := plainText(res.Content)
	if keepEscapes {
		content = hostStyledText(res.Content)
	}
	fmt.Fprintln(w, session.UntrustedFence(who, strings.TrimRight(content, "\n")))
	return nil
}

func queryWindows(sessionName string, jsonOutput, rail bool) error {
	t, err := dialSessionTarget(sessionName)
	if err != nil {
		return err
	}
	defer t.Close()

	params := map[string]any{"session": sessionName}
	if rail {
		params["rail"] = true
	}
	raw, err := t.client.Call("list-windows", t.params(params))
	if err != nil {
		return reportVerbError(t.explain("list-windows", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResultOn(t, raw, jsonOutput)
	}
	return printWindowList(raw, t.on())
}

func queryWindow(sessionName string, args []string, jsonOutput bool) error {
	window := ""
	if len(args) > 0 {
		window = args[0]
	}
	t, err := dialTarget(sessionName, window)
	if err != nil {
		return err
	}
	defer t.Close()

	raw, err := t.client.Call("get-window", t.params(map[string]any{"session": sessionName, "window": window}))
	if err != nil {
		return reportVerbError(t.explain("get-window", err), jsonOutput)
	}
	var data map[string]any
	if err := jsonx.Unmarshal(t.result(raw), &data); err != nil {
		return reportVerbError(errfmt.Errorf("failed to parse response: %w", err), jsonOutput)
	}
	delete(data, "type")
	if jsonOutput {
		out := map[string]any{"success": true, "message": "command executed"}
		maps.Copy(out, data)
		outputJSON(out)
		return nil
	}
	return printWindowDetail(data)
}

func printWindowList(raw json.RawMessage, on string) error {
	var res struct {
		Windows []windowRow `json:"windows"`
		Total   int         `json:"total"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	shown := res.Windows[:0]
	for _, w := range res.Windows {
		if w.Scratch {
			continue
		}
		shown = append(shown, w)
	}
	res.Windows = shown
	if len(res.Windows) == 0 {
		fmt.Println("No windows. Create one with 'tuios new-window'.")
		return nil
	}

	rows := make([][]string, 0, len(res.Windows))
	for _, w := range res.Windows {
		marker := ""
		if w.Focused {
			marker = "*"
		}
		rows = append(rows, []string{
			marker + fmt.Sprintf("%d", w.Index),
			shortWindowID(w.WindowID),
			windowLabel(w),
			fmt.Sprintf("%d", w.Workspace),
			fmt.Sprintf("%dx%d", w.Width, w.Height),
			w.AgentState,
		})
	}

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("8"))).
		Headers("IDX", "ID", "NAME", "WS", "SIZE", "AGENT").
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			base := lipgloss.NewStyle().Padding(0, 1)
			if row == table.HeaderRow {
				return base.Bold(true).Foreground(lipgloss.Color("12"))
			}
			switch col {
			case 2:
				return base.Foreground(lipgloss.Color("3")).Bold(true)
			case 1, 3, 4:
				return base.Foreground(lipgloss.Color("8"))
			default:
				return base
			}
		})

	fmt.Println(t.Render())
	fmt.Printf("\n%s%s. * marks the focused one.\n", plural.Count(len(res.Windows), "window"), on)
	return nil
}

func windowLabel(w windowRow) string {
	switch {
	case w.CustomName != "":
		return w.CustomName
	case w.Display != "":
		return w.Display
	default:
		return w.Title
	}
}

func shortWindowID(id string) string {
	const prefix = 8
	if len(id) <= prefix {
		return id
	}
	return id[:prefix]
}

func printWindowDetail(data map[string]any) error {
	encoded, err := jsonx.Marshal(data)
	if err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	var w windowRow
	if err := jsonx.Unmarshal(encoded, &w); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	fields := [][2]string{
		{"name", windowLabel(w)},
		{"id", w.WindowID},
		{"index", fmt.Sprintf("%d", w.Index)},
		{"title", w.Title},
		{"workspace", fmt.Sprintf("%d", w.Workspace)},
		{"size", fmt.Sprintf("%dx%d", w.Width, w.Height)},
		{"focused", fmt.Sprintf("%t", w.Focused)},
		{"minimized", fmt.Sprintf("%t", w.Minimized)},
		{"agent", w.AgentState},
	}
	if w.AgentMsg != "" {
		fields = append(fields, [2]string{"agent message", w.AgentMsg})
	}
	for _, f := range fields {
		fmt.Printf("%-14s %s\n", f[0], f[1])
	}
	return nil
}
