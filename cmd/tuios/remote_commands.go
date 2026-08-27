package main

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/Gaurav-Gosain/tuios/internal/capture"
	"github.com/Gaurav-Gosain/tuios/internal/harness"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/Gaurav-Gosain/tuios/internal/shot"
	"github.com/Gaurav-Gosain/tuios/internal/tape"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
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
		if err := json.Unmarshal(raw, &pretty); err != nil {
			return fmt.Errorf("failed to parse response: %w", err)
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
	if err := json.Unmarshal(raw, &catalog); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
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
	if err := json.Unmarshal(raw, &fields); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
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
func reportVerbError(err error, jsonOutput bool) error {
	if jsonOutput {
		outputJSON(map[string]any{"success": false, "error": err.Error()})
		os.Exit(1)
	}
	return err
}

// runSendKeys sends keystrokes to a running TUIOS session over the verb protocol.
func runSendKeys(sessionName, keys string, literal bool, raw bool, windowTarget string) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	if _, err := client.Call("send-keys", map[string]any{
		"session": sessionName,
		"window":  windowTarget,
		"keys":    keys,
		"literal": literal,
		"raw":     raw,
	}); err != nil {
		return explainVerbError("send-keys", err)
	}
	return nil
}

// runNewWindow opens a window in a session and reports its id, which is the
// handle every later call needs. workspace 0 means the current one, an empty
// cwd means the daemon's own directory, and a non-empty command is an argv the
// window execs as its process instead of a shell.
func runNewWindow(sessionName, name string, workspace int, cwd string, focus bool, command []string, jsonOutput bool) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	params := map[string]any{
		"session":   sessionName,
		"name":      name,
		"workspace": workspace,
		"cwd":       cwd,
		"focus":     focus,
	}
	if len(command) > 0 {
		params["command"] = command
	}
	raw, err := client.Call("new-window", params)
	if err != nil {
		return reportVerbError(explainVerbError("new-window", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, jsonOutput)
	}
	var res struct {
		WindowID string `json:"window_id"`
		Name     string `json:"name"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	fmt.Printf("%s  %s\n", shortWindowID(res.WindowID), res.Name)
	return nil
}

// runSplitWindow divides a pane and reports the id of the one the split made,
// which is the only way to address it without diffing two window lists.
func runSplitWindow(sessionName, windowTarget, direction, name string, jsonOutput bool) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	raw, err := client.Call("split-window", map[string]any{
		"session":   sessionName,
		"window":    windowTarget,
		"direction": direction,
		"name":      name,
	})
	if err != nil {
		return reportVerbError(explainVerbError("split-window", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, jsonOutput)
	}
	var res struct {
		WindowID string `json:"window_id"`
		Name     string `json:"name"`
		Note     string `json:"note"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	if res.WindowID == "" {
		fmt.Println(res.Note)
		return nil
	}
	fmt.Printf("%s  %s\n", shortWindowID(res.WindowID), res.Name)
	return nil
}

// runFocusWindow moves the focus and says where it landed. A relative or
// directional move does not name its pane in advance, so echoing the request
// would confirm nothing.
func runFocusWindow(sessionName, windowTarget, relative, direction string, jsonOutput bool) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	raw, err := client.Call("focus-window", map[string]any{
		"session":   sessionName,
		"window":    windowTarget,
		"relative":  relative,
		"direction": direction,
	})
	if err != nil {
		return reportVerbError(explainVerbError("focus-window", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, jsonOutput)
	}
	var res struct {
		FocusedWindowID string    `json:"focused_window_id"`
		Window          windowRow `json:"window"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	if res.FocusedWindowID == "" {
		fmt.Println("No window has the focus.")
		return nil
	}
	fmt.Printf("%s  %s\n", shortWindowID(res.FocusedWindowID), windowLabel(res.Window))
	return nil
}

// runMoveWindow moves a window to another workspace.
func runMoveWindow(sessionName, windowTarget string, workspace int, follow, jsonOutput bool) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	raw, err := client.Call("move-window", map[string]any{
		"session":   sessionName,
		"window":    windowTarget,
		"workspace": workspace,
		"follow":    follow,
	})
	if err != nil {
		return reportVerbError(explainVerbError("move-window", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, jsonOutput)
	}
	var res struct {
		WindowID string `json:"window_id"`
		From     int    `json:"from_workspace"`
		To       int    `json:"workspace"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	fmt.Printf("moved %s from workspace %d to %d\n", shortWindowID(res.WindowID), res.From, res.To)
	return nil
}

// runSetWindow changes a window's name or its minimized state. Both are
// pointers because an unset one is left alone, and an empty name is a request
// to clear it rather than a request to do nothing.
func runSetWindow(sessionName, windowTarget string, name *string, minimized *bool, jsonOutput bool) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

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

	raw, err := client.Call("set-window", params)
	if err != nil {
		return reportVerbError(explainVerbError("set-window", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, jsonOutput)
	}
	var res windowRow
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	state := "restored"
	if res.Minimized {
		state = "minimized"
	}
	fmt.Printf("%s  %s  %s\n", shortWindowID(res.WindowID), windowLabel(res), state)
	return nil
}

// runSelectWorkspace shows a workspace.
func runSelectWorkspace(sessionName string, workspace int, jsonOutput bool) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	raw, err := client.Call("select-workspace", map[string]any{
		"session":   sessionName,
		"workspace": workspace,
	})
	if err != nil {
		return reportVerbError(explainVerbError("select-workspace", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, jsonOutput)
	}
	var res struct {
		Current     int `json:"current_workspace"`
		WindowCount int `json:"window_count"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	fmt.Printf("workspace %d, %d window(s)\n", res.Current, res.WindowCount)
	return nil
}

// runListWorkspaces lists every workspace with what is on it.
func runListWorkspaces(sessionName string, jsonOutput bool) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	raw, err := client.Call("list-workspaces", map[string]any{"session": sessionName})
	if err != nil {
		return reportVerbError(explainVerbError("list-workspaces", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, jsonOutput)
	}
	return printWorkspaceList(raw)
}

// printWorkspaceList renders the workspace list in the same table shape as the
// window list, so the two read as one tool.
func printWorkspaceList(raw json.RawMessage) error {
	var res struct {
		Workspaces []struct {
			Workspace   int    `json:"workspace"`
			Name        string `json:"name"`
			WindowCount int    `json:"window_count"`
			Current     bool   `json:"current"`
		} `json:"workspaces"`
		Current int `json:"current_workspace"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	if len(res.Workspaces) == 0 {
		fmt.Println("No workspaces.")
		return nil
	}

	rows := make([][]string, 0, len(res.Workspaces))
	for _, ws := range res.Workspaces {
		marker := ""
		if ws.Current {
			marker = "*"
		}
		rows = append(rows, []string{
			marker + fmt.Sprintf("%d", ws.Workspace),
			ws.Name,
			fmt.Sprintf("%d", ws.WindowCount),
		})
	}

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("8"))).
		Headers("WS", "NAME", "WINDOWS").
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			base := lipgloss.NewStyle().Padding(0, 1)
			if row == table.HeaderRow {
				return base.Bold(true).Foreground(lipgloss.Color("12"))
			}
			switch col {
			case 1:
				return base.Foreground(lipgloss.Color("3")).Bold(true)
			case 2:
				return base.Foreground(lipgloss.Color("8"))
			default:
				return base
			}
		})

	fmt.Println(t.Render())
	fmt.Printf("\n%d workspace(s). * marks the one showing.\n", len(res.Workspaces))
	return nil
}

// runSetLayout turns tiling on or off and tidies the splits. tiling is a
// pointer so a call that only equalizes leaves the tiling mode alone.
func runSetLayout(sessionName string, tiling *bool, equalize, rotate, jsonOutput bool) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	params := map[string]any{
		"session":  sessionName,
		"equalize": equalize,
		"rotate":   rotate,
	}
	if tiling != nil {
		params["tiling"] = *tiling
	}

	raw, err := client.Call("set-layout", params)
	if err != nil {
		return reportVerbError(explainVerbError("set-layout", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, jsonOutput)
	}
	var res struct {
		TilingMode  string  `json:"tiling_mode"`
		LayoutMode  string  `json:"layout_mode"`
		MasterRatio float64 `json:"master_ratio"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	fmt.Printf("%s  layout %s  master %.2f\n", res.TilingMode, res.LayoutMode, res.MasterRatio)
	return nil
}

// runSendText writes text verbatim to a pane's PTY. Unlike send-keys it parses
// nothing, so a trailing newline in the argument is the Enter that submits the
// line, and one call is enough to type and run a command.
func runSendText(sessionName, windowTarget, text string) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	if _, err := client.Call("send-text", map[string]any{
		"session": sessionName,
		"window":  windowTarget,
		"text":    text,
	}); err != nil {
		return explainVerbError("send-text", err)
	}
	return nil
}

// runCapturePane captures the content of a pane and prints to stdout. lines
// keeps only the last N lines when positive, which is what bounds a capture of a
// long scrollback to something a caller can actually read.
func runCapturePane(sessionName, windowTarget string, scrollback, ansi, resolved bool, palette []string, lines int) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	raw, err := client.Call("capture-pane", map[string]any{
		"session":    sessionName,
		"window":     windowTarget,
		"scrollback": scrollback,
		"ansi":       ansi,
		"resolved":   resolved,
		"palette":    palette,
		"lines":      lines,
	})
	if err != nil {
		return explainVerbError("capture-pane", err)
	}

	var res struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	fmt.Print(res.Content)
	return nil
}

// runCommand executes a tape command in a running TUIOS session.
func runCommand(sessionName, command string, args []string, jsonOutput bool) error {
	return runCommandRendered(sessionName, command, args, jsonOutput, nil)
}

// runCommandRendered is runCommand with a printer for the human output. A
// command that answers with data, rather than only succeeding, passes one so the
// answer is shown instead of thrown away; render is nil for the rest.
func runCommandRendered(sessionName, command string, args []string, jsonOutput bool, render resultRenderer) error {
	if err := requireDaemon(); err != nil {
		return err
	}

	client := session.NewClient(&session.ClientConfig{
		Version: version,
	})

	if err := client.Connect(); err != nil {
		return explainDialError(err)
	}
	defer func() { _ = client.Close() }()

	requestID := uuid.New().String()

	// Send the execute command
	msg, err := session.NewMessage(session.MsgExecuteCommand, &session.ExecuteCommandPayload{
		SessionName: sessionName,
		CommandType: command,
		Args:        args,
		RequestID:   requestID,
	})
	if err != nil {
		return fmt.Errorf("failed to create message: %w", err)
	}

	if err := sendAndWaitForResultWithFormat(client, msg, requestID, jsonOutput, render); err != nil {
		return err
	}

	return nil
}

// queryWindows queries the window list over the verb protocol (no TUI required).
func queryWindows(sessionName string, jsonOutput bool) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	raw, err := client.Call("list-windows", map[string]any{"session": sessionName})
	if err != nil {
		return reportVerbError(explainVerbError("list-windows", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, jsonOutput)
	}
	return printWindowList(raw)
}

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
	AgentState string `json:"agent_state"`
	AgentMsg   string `json:"agent_message"`
	Width      int    `json:"width"`
	Height     int    `json:"height"`
}

// printWindowList renders the window list as a table. Without this the command
// printed only that it had succeeded, which told a reader nothing they asked
// for and made --json the only way to see a window.
func printWindowList(raw json.RawMessage) error {
	var res struct {
		Windows []windowRow `json:"windows"`
		Total   int         `json:"total"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
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
	fmt.Printf("\n%d window(s). * marks the focused one.\n", res.Total)
	return nil
}

// windowLabel is the name to show for a window: the name it was given, else
// whatever its shell set as the title.
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

// shortWindowID trims a window uuid to the prefix that addresses it. The verb
// protocol resolves a window from 8 or more leading characters, so this is the
// shortest form that can be pasted back into another command.
func shortWindowID(id string) string {
	const prefix = 8
	if len(id) <= prefix {
		return id
	}
	return id[:prefix]
}

// printWindowDetail renders one window as labelled lines.
func printWindowDetail(data map[string]any) error {
	encoded, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	var w windowRow
	if err := json.Unmarshal(encoded, &w); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
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

// querySession queries session info over the verb protocol (no TUI required).
func querySession(sessionName string, jsonOutput bool) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	raw, err := client.Call("session-info", map[string]any{"session": sessionName})
	if err != nil {
		return reportVerbError(explainVerbError("session-info", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, jsonOutput)
	}
	return printSessionInfo(raw)
}

// printSessionInfo renders session details as labelled lines, for the same
// reason printWindowList exists.
func printSessionInfo(raw json.RawMessage) error {
	var res struct {
		Name             string         `json:"session_name"`
		DisplayName      string         `json:"display_name"`
		Accent           string         `json:"accent"`
		CurrentWorkspace int            `json:"current_workspace"`
		NumWorkspaces    int            `json:"num_workspaces"`
		WorkspaceNames   map[string]any `json:"workspace_names"`
		WorkspaceOrder   []int          `json:"workspace_order"`
		WindowCount      int            `json:"window_count"`
		TilingMode       string         `json:"tiling_mode"`
		Width            int            `json:"width"`
		Height           int            `json:"height"`
		TUIAttached      bool           `json:"tui_attached"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	fields := [][2]string{
		{"session", res.Name},
	}
	if res.DisplayName != "" {
		fields = append(fields, [2]string{"display name", res.DisplayName})
	}
	if res.Accent != "" {
		fields = append(fields, [2]string{"accent", res.Accent})
	}
	fields = append(fields,
		[2]string{"windows", fmt.Sprintf("%d", res.WindowCount)},
		[2]string{"workspace", fmt.Sprintf("%d of %d", res.CurrentWorkspace, res.NumWorkspaces)},
		[2]string{"tiling", res.TilingMode},
		[2]string{"size", fmt.Sprintf("%dx%d", res.Width, res.Height)},
		[2]string{"attached", fmt.Sprintf("%t", res.TUIAttached)},
	)
	for _, f := range fields {
		fmt.Printf("%-14s %s\n", f[0], f[1])
	}

	if len(res.WorkspaceNames) > 0 {
		named := make([]string, 0, len(res.WorkspaceNames))
		for num, name := range res.WorkspaceNames {
			named = append(named, fmt.Sprintf("%s=%v", num, name))
		}
		sort.Strings(named)
		fmt.Printf("%-14s %s\n", "named", strings.Join(named, " "))
	}
	// Only a rearranged session prints a row here. The order is presentation, so
	// the workspaces keep the numbers everything else addresses them by whatever
	// this says.
	if len(res.WorkspaceOrder) > 0 {
		shown := make([]string, 0, len(res.WorkspaceOrder))
		for _, ws := range res.WorkspaceOrder {
			shown = append(shown, strconv.Itoa(ws))
		}
		fmt.Printf("%-14s %s\n", "order", strings.Join(shown, " "))
	}
	return nil
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
	Deprecated  string   `json:"deprecated"`
	SessionVal  string   `json:"session_value"`
}

// runListOptions lists the settable configuration paths. It is the command that
// answers "what can I set", so the human form prints the whole contract of each
// option rather than a bare path a caller would still have to look up.
func runListOptions(sessionName, section, prefix string, jsonOutput bool) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	raw, err := client.Call("list-options", map[string]any{
		"session": sessionName,
		"section": section,
		"prefix":  prefix,
	})
	if err != nil {
		return reportVerbError(explainVerbError("list-options", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, jsonOutput)
	}
	var res struct {
		Options  []optionRow `json:"options"`
		Sections []string    `json:"sections"`
		Total    int         `json:"total"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	printOptionList(os.Stdout, res.Options, res.Sections, res.Total)
	return nil
}

// printOptionList writes the options grouped by section, then the section names
// so a reader who found the list too long can narrow it without a second guess.
func printOptionList(w io.Writer, options []optionRow, sections []string, total int) {
	if len(options) == 0 {
		fmt.Fprintln(w, "No options match that filter.")
		return
	}

	// One width across every group, so the paths line up down the whole page
	// rather than shifting at each section heading.
	width := 0
	for _, opt := range options {
		if len(opt.Path) > width {
			width = len(opt.Path)
		}
	}

	current := ""
	for _, opt := range options {
		if opt.Section != current {
			if current != "" {
				fmt.Fprintln(w)
			}
			current = opt.Section
			fmt.Fprintf(w, "[%s]\n", current)
		}
		deprecated := ""
		if opt.Deprecated != "" {
			deprecated = "  (deprecated)"
		}
		fmt.Fprintf(w, "  %-*s  %-6s  default %s%s\n", width, opt.Path, opt.Type, orNone(opt.Default), deprecated)
		fmt.Fprintf(w, "  %-*s  %s\n", width, "", opt.Description)
		if len(opt.Accepted) > 0 {
			fmt.Fprintf(w, "  %-*s  one of: %s\n", width, "", strings.Join(opt.Accepted, ", "))
		}
		if opt.Max > 0 {
			fmt.Fprintf(w, "  %-*s  range: %d to %d\n", width, "", opt.Min, opt.Max)
		}
		if opt.SessionVal != "" {
			fmt.Fprintf(w, "  %-*s  this session: %s\n", width, "", opt.SessionVal)
		}
		if opt.Deprecated != "" {
			fmt.Fprintf(w, "  %-*s  %s\n", width, "", opt.Deprecated)
		}
	}

	fmt.Fprintf(w, "\n%d option(s). Set one with 'tuios set-config <path> <value>'.\n", total)
	if len(sections) > 0 {
		fmt.Fprintf(w, "Sections: %s\n", strings.Join(sections, ", "))
		fmt.Fprintln(w, "Narrow with --section <name>, or pass a path prefix as the argument.")
	}
}

// runSetConfig sets a session option over the verb protocol. The value is
// recorded in daemon-owned state and, when a TUI is attached, applied live.
func runSetConfig(sessionName, path, value string) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	if _, err := client.Call("set-option", map[string]any{
		"session": sessionName,
		"key":     path,
		"value":   value,
	}); err != nil {
		return explainVerbError("set-option", err)
	}
	fmt.Printf("Set %s = %s\n", path, value)
	return nil
}

// runGetConfig reads a session option over the verb protocol.
func runGetConfig(sessionName, path string, jsonOutput bool) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	raw, err := client.Call("get-option", map[string]any{
		"session": sessionName,
		"key":     path,
	})
	if err != nil {
		return explainVerbError("get-option", err)
	}
	// The bare value stays the default output: it is what a shell substitution
	// wants, and printing anything else there would break every script using it.
	if jsonOutput {
		var pretty any
		if err := json.Unmarshal(raw, &pretty); err != nil {
			return fmt.Errorf("failed to parse response: %w", err)
		}
		out, err := json.MarshalIndent(pretty, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to render response: %w", err)
		}
		fmt.Println(string(out))
		return nil
	}
	var res struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	fmt.Println(res.Value)
	return nil
}

// runSetAgentState reports a pane's agent state to the daemon over the verb
// protocol. It is what the reference shim calls, and what a user runs to mark a
// pane by hand.
//
// A report the daemon declines because a higher-ranked source already owns the
// window comes back as applied:false, not as an error. Saying so matters: the
// caller otherwise believes it set a state that never took.
func runSetAgentState(sessionName, windowTarget, state, message, source, harness string) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	raw, err := client.Call("set-agent-state", map[string]any{
		"session": sessionName,
		"window":  windowTarget,
		"state":   state,
		"message": message,
		"source":  source,
		"harness": harness,
	})
	if err != nil {
		return explainVerbError("set-agent-state", err)
	}

	var res struct {
		Applied bool   `json:"applied"`
		State   string `json:"state"`
		Source  string `json:"source"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	if !res.Applied {
		fmt.Fprintf(os.Stderr, "Not applied: a higher-ranked source owns this pane. It still reports %s.\n", res.State)
	}
	return nil
}

// runSetSessionName sets a session's display label. The session keeps its own
// name for addressing, so renaming the label never breaks a script.
func runSetSessionName(sessionName, name string) error {
	return callAndReport("set-session-name", map[string]any{
		"session": sessionName,
		"name":    name,
	}, func(res map[string]any) {
		if name == "" {
			fmt.Printf("Cleared the display name of session %v.\n", res["session"])
			return
		}
		fmt.Printf("Session %v now shows as %q.\n", res["session"], res["display_name"])
	})
}

// runSetSessionAccent sets a session's accent slot, shared by every attached
// client.
func runSetSessionAccent(sessionName, accent string) error {
	return callAndReport("set-session-accent", map[string]any{
		"session": sessionName,
		"accent":  accent,
	}, func(res map[string]any) {
		if accent == "" {
			fmt.Printf("Cleared the accent of session %v.\n", res["session"])
			return
		}
		fmt.Printf("Session %v now uses accent %v.\n", res["session"], res["accent"])
	})
}

// runSetWorkspaceName labels a workspace. The number stays its identity.
func runSetWorkspaceName(sessionName string, workspace int, name string) error {
	return callAndReport("set-workspace-name", map[string]any{
		"session":   sessionName,
		"workspace": workspace,
		"name":      name,
	}, func(res map[string]any) {
		if name == "" {
			fmt.Printf("Cleared the name of workspace %v.\n", res["workspace"])
			return
		}
		fmt.Printf("Workspace %v is now named %q.\n", res["workspace"], res["name"])
	})
}

// callAndReport makes a one-shot verb call and hands the decoded result to a
// printer, so the small setter commands do not each repeat the dial, the error
// wrapping, and the decode.
func callAndReport(verb string, params map[string]any, report func(map[string]any)) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	raw, err := client.Call(verb, params)
	if err != nil {
		return explainVerbError(verb, err)
	}
	var res map[string]any
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	report(res)
	return nil
}

// runWaitFor blocks until a daemon-side condition matches, so a caller can stop
// polling a pane and sleeping between captures.
//
// The read deadline is stretched past the requested timeout because the daemon
// only answers once the wait resolves: a client deadline shorter than the wait
// would report a connection failure for a wait that was still perfectly healthy.
func runWaitFor(sessionName, windowTarget, condition, pattern, until string, idle, timeout int, jsonOutput bool) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	params := map[string]any{
		"session":   sessionName,
		"window":    windowTarget,
		"condition": condition,
		"pattern":   pattern,
		"timeout":   timeout,
	}
	if until != "" {
		params["until"] = until
	}
	if idle > 0 {
		params["idle"] = idle
	}

	grace := time.Duration(timeout)*time.Millisecond + 10*time.Second
	raw, err := client.CallWithTimeout("wait-for", params, grace)
	if err != nil {
		return reportVerbError(explainVerbError("wait-for", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, jsonOutput)
	}

	var res struct {
		Condition string `json:"condition"`
		Window    string `json:"window"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	if res.Window != "" {
		fmt.Printf("%s matched on %s\n", res.Condition, res.Window)
		return nil
	}
	fmt.Printf("%s matched\n", res.Condition)
	return nil
}

// runGetAgentState reads a pane's reported agent state and prints it. With
// jsonOutput it prints the full result; otherwise it prints the state name.
func runGetAgentState(sessionName, windowTarget string, jsonOutput bool) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	raw, err := client.Call("get-agent-state", map[string]any{
		"session": sessionName,
		"window":  windowTarget,
	})
	if err != nil {
		return reportVerbError(explainVerbError("get-agent-state", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, jsonOutput)
	}
	var res struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	fmt.Println(res.State)
	return nil
}

// detectExplanation is the explain-agent-detect result, decoded for printing.
type detectExplanation struct {
	WindowID       string                 `json:"window_id"`
	State          string                 `json:"state"`
	Source         string                 `json:"source"`
	HarnessID      string                 `json:"harness_id"`
	AutoDetected   bool                   `json:"auto_detected"`
	Running        bool                   `json:"running"`
	Reason         string                 `json:"reason"`
	Process        harness.ProcReport     `json:"process"`
	Manifests      []harness.DetectReport `json:"manifests"`
	NameList       string                 `json:"name_list"`
	Matched        bool                   `json:"matched"`
	MatchedHarness string                 `json:"matched_harness"`
	MatchedRule    string                 `json:"matched_rule"`
	Note           string                 `json:"note"`
}

// runExplainAgentDetect prints what the detector saw in a pane and what every
// manifest made of it.
func runExplainAgentDetect(sessionName, windowTarget string, jsonOutput bool) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	raw, err := client.Call("explain-agent-detect", map[string]any{
		"session": sessionName,
		"window":  windowTarget,
	})
	if err != nil {
		return reportVerbError(explainVerbError("explain-agent-detect", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, jsonOutput)
	}
	var res detectExplanation
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	printDetectExplanation(os.Stdout, res)
	return nil
}

// printDetectExplanation writes the human form: what the pane is attributed to,
// what the detector read, and what each manifest did with it.
func printDetectExplanation(w io.Writer, res detectExplanation) {
	harnessName := res.HarnessID
	if harnessName == "" {
		harnessName = "none"
	}
	fmt.Fprintf(w, "pane %s  state %s (%s)\nharness %s", res.WindowID, res.State, res.Source, harnessName)
	if res.AutoDetected {
		fmt.Fprint(w, "  held by auto-detection")
	}
	fmt.Fprintln(w)

	if !res.Running {
		fmt.Fprintf(w, "\n%s\n", res.Reason)
		return
	}

	p := res.Process
	fmt.Fprintln(w, "\nwhat the detector read:")
	fmt.Fprintf(w, "  comm   %s\n", orNone(p.Comm))
	fmt.Fprintf(w, "  argv   %s\n", orNone(strings.Join(p.Argv, " ")))
	fmt.Fprintf(w, "  exe    %s\n", orNone(p.Exe))
	fmt.Fprintf(w, "  argv0  %s\n", orNone(p.Argv0))
	switch {
	case p.RunToken != "":
		fmt.Fprintf(w, "  run    %s  (an interpreter, so this one token may name an agent)\n", p.RunToken)
	case p.Interpreter:
		fmt.Fprintln(w, "  run    (none: an interpreter, but it was given nothing to run)")
	default:
		fmt.Fprintln(w, "  run    (none: not an interpreter, so no argument is read as identity)")
	}

	fmt.Fprintln(w, "\nmanifests, in lookup order:")
	for _, m := range res.Manifests {
		if m.Matched {
			fmt.Fprintf(w, "  * %-14s matched on %s\n", m.ID, m.Rule)
			continue
		}
		fmt.Fprintf(w, "    %-14s %s\n", m.ID, m.Reason)
	}

	fmt.Fprintln(w)
	switch {
	case res.Matched && res.MatchedHarness != "":
		fmt.Fprintf(w, "result: %s, on %s\n", res.MatchedHarness, res.MatchedRule)
	case res.Matched:
		fmt.Fprintf(w, "result: an agent, on %s\n", res.MatchedRule)
		if res.Note != "" {
			fmt.Fprintf(w, "        %s\n", res.Note)
		}
	default:
		fmt.Fprintln(w, "result: not an agent")
		if res.NameList != "" {
			fmt.Fprintf(w, "        the name list alone would have matched: %s\n", res.NameList)
		}
	}
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
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
}

// runExplainAgentScreen prints what a harness's screen rules make of a pane.
func runExplainAgentScreen(sessionName, windowTarget, harnessID string, lines int, jsonOutput bool) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	raw, err := client.Call("explain-agent-screen", map[string]any{
		"session": sessionName,
		"window":  windowTarget,
		"harness": harnessID,
		"lines":   lines,
	})
	if err != nil {
		return reportVerbError(explainVerbError("explain-agent-screen", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, jsonOutput)
	}
	var res screenExplanation
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	printScreenExplanation(os.Stdout, res)
	return nil
}

// printScreenExplanation writes the human form: what the pane is, what the
// classifier read, and what each rule did with it.
func printScreenExplanation(w io.Writer, res screenExplanation) {
	harnessName := res.HarnessID
	if harnessName == "" {
		harnessName = "none"
	}
	fmt.Fprintf(w, "pane %s  state %s (%s)\nharness %s", res.WindowID, res.State, res.Source, harnessName)
	if res.HarnessID != "" {
		if res.Enabled {
			fmt.Fprintf(w, "  screen rules on, reading %d lines", res.Lines)
		} else {
			fmt.Fprint(w, "  screen rules off in the manifest")
		}
	}
	fmt.Fprintln(w)

	fmt.Fprintln(w, "\ntail, as the classifier sees it:")
	if len(res.Tail) == 0 {
		fmt.Fprintln(w, "  (nothing: the pane's visible screen is empty)")
	}
	for i, line := range res.Tail {
		fmt.Fprintf(w, "  %2d | %s\n", i+1, line)
	}

	if res.HarnessID == "" {
		fmt.Fprintln(w, "\nno harness is attributed to this pane, so no rules ran.")
		fmt.Fprintln(w, "pass --harness to try one's rules against the tail above.")
		return
	}
	if len(res.Rules) == 0 {
		fmt.Fprintf(w, "\n%s declares no screen rules.\n", res.HarnessID)
		return
	}
	fmt.Fprintln(w, "\nrules:")
	for _, r := range res.Rules {
		mark := " "
		if r.Matched {
			mark = "*"
			if r.Index == res.Rule {
				mark = ">"
			}
		}
		fmt.Fprintf(w, " %s rule %d  %s  priority %d\n", mark, r.Index, r.State, r.Priority)
		for _, why := range ruleRefusals(r) {
			fmt.Fprintf(w, "     %s\n", why)
		}
	}
	// The leading mark is only readable next to what it means.
	fmt.Fprintln(w, "\n  > the rule that decided, * matched but outranked")
	if res.Matched {
		fmt.Fprintf(w, "  rule %d would report %s\n", res.Rule, res.RuleState)
	} else {
		fmt.Fprintln(w, "  no rule matched, so the screen rules report nothing")
	}
}

// ruleRefusals turns a rule's report into the lines saying why it refused.
func ruleRefusals(r harness.RuleReport) []string {
	var out []string
	if r.Empty {
		out = append(out, "names no strings, so it is refused rather than matching every pane")
	}
	for _, s := range r.Missing {
		out = append(out, "all: "+strconv.Quote(s)+" is not on the screen")
	}
	if len(r.NoneOf) > 0 {
		quoted := make([]string, 0, len(r.NoneOf))
		for _, s := range r.NoneOf {
			quoted = append(quoted, strconv.Quote(s))
		}
		out = append(out, "any: none of "+strings.Join(quoted, ", ")+" is on the screen")
	}
	for _, s := range r.Blocked {
		out = append(out, "not: "+strconv.Quote(s)+" is on the screen and vetoes the rule")
	}
	for _, s := range r.MissingRegex {
		out = append(out, "regex: "+strconv.Quote(s)+" matches nothing on the screen")
	}
	for _, s := range r.BlockedRegex {
		out = append(out, "not_regex: "+strconv.Quote(s)+" matches the screen and vetoes the rule")
	}
	return out
}

// runTapeExec executes a tape file in a running TUIOS session.
func runTapeExec(sessionName, filePath string) error {
	if err := requireDaemon(); err != nil {
		return err
	}

	// Read the tape file
	content, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read tape file: %w", err)
	}
	script := string(content)

	// Validate the script first
	lexer := tape.New(script)
	parser := tape.NewParser(lexer)
	commands := parser.Parse()

	if len(commands) == 0 {
		return fmt.Errorf("tape script has no commands or contains errors")
	}

	client := session.NewClient(&session.ClientConfig{
		Version: version,
	})

	if err := client.Connect(); err != nil {
		return explainDialError(err)
	}
	defer func() { _ = client.Close() }()

	requestID := uuid.New().String()

	// Send the execute command with tape script
	msg, err := session.NewMessage(session.MsgExecuteCommand, &session.ExecuteCommandPayload{
		SessionName: sessionName,
		TapeScript:  script,
		RequestID:   requestID,
	})
	if err != nil {
		return fmt.Errorf("failed to create message: %w", err)
	}

	if err := sendAndWaitForResult(client, msg, requestID); err != nil {
		return err
	}

	return nil
}

// sendAndWaitForResult sends a message and waits for the result (human-readable output).
func sendAndWaitForResult(client *session.Client, msg *session.Message, requestID string) error {
	return sendAndWaitForResultWithFormat(client, msg, requestID, false, nil)
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
		return fmt.Errorf("failed to send command: %w", err)
	}

	// Check response type
	switch resp.Type {
	case session.MsgCommandResult:
		var result session.CommandResultPayload
		if err := resp.ParsePayloadWithCodec(&result, client.GetCodec()); err != nil {
			if jsonOutput {
				outputJSON(map[string]any{
					"success": false,
					"error":   fmt.Sprintf("failed to parse response: %v", err),
				})
				return nil
			}
			return fmt.Errorf("failed to parse response: %w", err)
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
			return fmt.Errorf("command failed: %s", result.Message)
		}
		if render != nil {
			return render(result.Data)
		}
		fmt.Printf("Command executed successfully: %s\n", result.Message)
		return nil

	case session.MsgError:
		var errPayload session.ErrorPayload
		if err := resp.ParsePayloadWithCodec(&errPayload, client.GetCodec()); err != nil {
			if jsonOutput {
				outputJSON(map[string]any{
					"success": false,
					"error":   "command failed with unknown error",
				})
				return nil
			}
			return fmt.Errorf("command failed with unknown error")
		}
		if jsonOutput {
			outputJSON(map[string]any{
				"success": false,
				"error":   errPayload.Message,
			})
			os.Exit(1)
			return nil
		}
		return fmt.Errorf("command failed: %s", errPayload.Message)

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
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// listAvailableCommands lists all available tape commands that can be executed remotely.
func listAvailableCommands() {
	commands := []struct {
		name        string
		description string
		example     string
	}{
		// Window management
		{"NewWindow [name]", "Create a new terminal window", "tuios run-command NewWindow \"My Terminal\""},
		{"CloseWindow [name]", "Close window(s) - all matching if name given", "tuios run-command CloseWindow \"Build\""},
		{"NextWindow", "Focus the next window", "tuios run-command NextWindow"},
		{"PrevWindow", "Focus the previous window", "tuios run-command PrevWindow"},
		{"FocusWindow <name>", "Focus a window by name", "tuios run-command FocusWindow \"Server\""},
		{"RenameWindow <name> | <old> <new>", "Rename focused or named window", "tuios run-command RenameWindow \"Old\" \"New\""},
		{"MinimizeWindow [name]", "Minimize focused or named window", "tuios run-command MinimizeWindow \"Server\""},
		{"RestoreWindow [name]", "Restore focused or named window", "tuios run-command RestoreWindow \"Server\""},

		// Mode switching
		{"TerminalMode", "Switch to terminal mode", "tuios run-command TerminalMode"},
		{"WindowManagementMode", "Switch to window management mode", "tuios run-command WindowManagementMode"},

		// Tiling
		{"ToggleTiling", "Toggle tiling mode", "tuios run-command ToggleTiling"},
		{"EnableTiling", "Enable tiling mode", "tuios run-command EnableTiling"},
		{"DisableTiling", "Disable tiling mode", "tuios run-command DisableTiling"},
		{"SnapLeft", "Snap focused window to left", "tuios run-command SnapLeft"},
		{"SnapRight", "Snap focused window to right", "tuios run-command SnapRight"},
		{"SnapFullscreen", "Snap focused window to fullscreen", "tuios run-command SnapFullscreen"},

		// BSP Tiling
		{"Split horizontal", "Split focused window horizontally", "tuios run-command Split horizontal"},
		{"Split vertical", "Split focused window vertically", "tuios run-command Split vertical"},
		{"RotateSplit", "Rotate the split direction", "tuios run-command RotateSplit"},
		{"EqualizeSplits", "Equalize all split ratios", "tuios run-command EqualizeSplits"},
		{"Screenshot", "Save the focused window as an image", "tuios run-command Screenshot"},

		// Workspace
		{"SwitchWorkspace 1-9", "Switch to workspace N", "tuios run-command SwitchWorkspace 2"},
		{"MoveToWorkspace 1-9", "Move focused window to workspace N", "tuios run-command MoveToWorkspace 3"},

		// Animations
		{"EnableAnimations", "Enable UI animations", "tuios run-command EnableAnimations"},
		{"DisableAnimations", "Disable UI animations", "tuios run-command DisableAnimations"},
		{"ToggleAnimations", "Toggle UI animations", "tuios run-command ToggleAnimations"},

		// Config commands
		{"SetDockbarPosition top|bottom|left|right", "Change dockbar position", "tuios run-command SetDockbarPosition top"},
		{"SetBorderStyle style", "Change window border style", "tuios run-command SetBorderStyle rounded"},
		{"SetTheme themename", "Change the color theme", "tuios run-command SetTheme dracula"},
		{"ShowNotification message [type]", "Show a notification", "tuios run-command ShowNotification \"Hello!\" info"},

		// Inspection commands
		{"ListWindows", "List all windows (use --json)", "tuios list-windows --json"},
		{"GetWindow [id-or-name]", "Get window info (use --json)", "tuios get-window --json"},
		{"GetSessionInfo", "Get session info (use --json)", "tuios session-info --json"},
	}

	fmt.Println("Available commands for 'tuios run-command':")
	fmt.Println()

	for _, cmd := range commands {
		fmt.Printf("  %-35s %s\n", cmd.name, cmd.description)
	}

	fmt.Println()
	fmt.Println("Examples:")
	for _, cmd := range commands {
		if cmd.example != "" {
			fmt.Printf("  %s\n", cmd.example)
		}
	}
}

// Completion functions for shell autocompletion

// fixedCompletions completes a flag whose values are a closed set the daemon
// will reject anything outside of, so the shell can offer them all.
func fixedCompletions(values ...string) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
		return values, cobra.ShellCompDirectiveNoFileComp
	}
}

// getSendKeysCompletions returns completions for send-keys key names.
func getSendKeysCompletions(toComplete string) []string {
	keys := []string{
		// Special tokens
		"$PREFIX\tConfigured leader/prefix key",
		"PREFIX\tConfigured leader/prefix key",
		// Special keys
		"Enter\tPress Enter/Return",
		"Return\tPress Enter/Return",
		"Space\tPress Space",
		"Tab\tPress Tab",
		"Escape\tPress Escape",
		"Esc\tPress Escape",
		"Backspace\tPress Backspace",
		"Delete\tPress Delete",
		// Arrow keys
		"Up\tPress Up arrow",
		"Down\tPress Down arrow",
		"Left\tPress Left arrow",
		"Right\tPress Right arrow",
		// Navigation
		"Home\tPress Home",
		"End\tPress End",
		"PageUp\tPress Page Up",
		"PageDown\tPress Page Down",
		"Insert\tPress Insert",
		// Function keys
		"F1\tPress F1",
		"F2\tPress F2",
		"F3\tPress F3",
		"F4\tPress F4",
		"F5\tPress F5",
		"F6\tPress F6",
		"F7\tPress F7",
		"F8\tPress F8",
		"F9\tPress F9",
		"F10\tPress F10",
		"F11\tPress F11",
		"F12\tPress F12",
		// Common key combos
		"ctrl+b\tPrefix key (default)",
		"ctrl+c\tInterrupt/cancel",
		"ctrl+d\tEOF/logout",
		"ctrl+z\tSuspend",
		"alt+1\tWorkspace 1",
		"alt+2\tWorkspace 2",
		"alt+3\tWorkspace 3",
		// Mode keys
		"i\tEnter terminal mode",
		"n\tNew window (in window mode)",
		"q\tQuit (in window mode)",
		"h\tMove left",
		"j\tMove down",
		"k\tMove up",
		"l\tMove right",
	}

	var filtered []string
	toComplete = strings.ToLower(toComplete)
	for _, key := range keys {
		if toComplete == "" || strings.HasPrefix(strings.ToLower(key), toComplete) {
			filtered = append(filtered, key)
		}
	}
	return filtered
}

// getRunCommandCompletions returns completions for run-command command names.
func getRunCommandCompletions(toComplete string) []string {
	commands := []string{
		"NewWindow\tCreate a new terminal window",
		"CloseWindow\tClose the focused window",
		"NextWindow\tFocus the next window",
		"PrevWindow\tFocus the previous window",
		"RenameWindow\tRename the focused window",
		"MinimizeWindow\tMinimize the focused window",
		"RestoreWindow\tRestore the focused window",
		"TerminalMode\tSwitch to terminal mode",
		"WindowManagementMode\tSwitch to window management mode",
		"ToggleTiling\tToggle tiling mode",
		"EnableTiling\tEnable tiling mode",
		"DisableTiling\tDisable tiling mode",
		"SnapLeft\tSnap window to left",
		"SnapRight\tSnap window to right",
		"SnapFullscreen\tSnap window fullscreen",
		"Split\tSplit window (horizontal/vertical)",
		"RotateSplit\tRotate split direction",
		"EqualizeSplits\tEqualize all splits",
		"SwitchWorkspace\tSwitch to workspace N",
		"MoveToWorkspace\tMove window to workspace N",
		"MoveAndFollowWorkspace\tMove and follow to workspace N",
		"EnableAnimations\tEnable animations",
		"DisableAnimations\tDisable animations",
		"ToggleAnimations\tToggle animations",
		"SetConfig\tSet a config option",
		"SetTheme\tChange theme",
		"SetDockbarPosition\tChange dockbar position",
		"SetBorderStyle\tChange border style",
		"ShowNotification\tShow a notification",
		"FocusDirection\tFocus window in direction",
		"Screenshot\tSave the focused window as an image",
	}

	var filtered []string
	toComplete = strings.ToLower(toComplete)
	for _, cmd := range commands {
		if toComplete == "" || strings.HasPrefix(strings.ToLower(cmd), toComplete) {
			filtered = append(filtered, cmd)
		}
	}
	return filtered
}

// getRunCommandArgCompletions returns completions for run-command arguments.
func getRunCommandArgCompletions(command string, argIndex int, toComplete string) []string {
	switch command {
	case "Split":
		if argIndex == 1 {
			return []string{"horizontal\tSplit top/bottom", "vertical\tSplit left/right"}
		}
	case "SwitchWorkspace", "MoveToWorkspace", "MoveAndFollowWorkspace":
		if argIndex == 1 {
			return []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}
		}
	case "SetDockbarPosition":
		if argIndex == 1 {
			return []string{"top", "bottom", "hidden"}
		}
	case "SetBorderStyle":
		if argIndex == 1 {
			return []string{"rounded", "normal", "thick", "double", "hidden", "block", "ascii"}
		}
	case "FocusDirection":
		if argIndex == 1 {
			return []string{"left", "right", "up", "down"}
		}
	case "ShowNotification":
		if argIndex == 2 {
			return []string{"info", "success", "warning", "error"}
		}
	case "SetConfig":
		if argIndex == 1 {
			return getConfigPathCompletions(toComplete)
		}
		if argIndex == 2 {
			// Would need the first arg to determine values
			return nil
		}
	}
	return nil
}

// getConfigPathCompletions returns completions for set-config paths.
func getConfigPathCompletions(toComplete string) []string {
	paths := []string{
		"dockbar_position\tDockbar position (top/bottom/hidden)",
		"border_style\tWindow border style",
		"animations\tEnable/disable animations (true/false/toggle)",
		"hide_window_buttons\tHide window buttons (true/false)",
		"window_button_style\tWindow control style (pill/dots)",
		"window_button_position\tWhich end the window controls sit on (right/left)",
	}

	var filtered []string
	toComplete = strings.ToLower(toComplete)
	for _, path := range paths {
		if toComplete == "" || strings.HasPrefix(strings.ToLower(path), toComplete) {
			filtered = append(filtered, path)
		}
	}
	return filtered
}

// getConfigValueCompletions returns completions for set-config values.
func getConfigValueCompletions(path, _ string) []string {
	switch path {
	case "dockbar_position", "appearance.dockbar_position":
		return []string{"top", "bottom", "hidden"}
	case "border_style", "appearance.border_style":
		return []string{"rounded", "normal", "thick", "double", "hidden", "block", "ascii"}
	case "animations", "appearance.animations_enabled", "animations_enabled":
		return []string{"true", "false", "toggle", "on", "off"}
	case "hide_window_buttons", "appearance.hide_window_buttons":
		return []string{"true", "false"}
	case "window_button_style", "appearance.window_button_style":
		return []string{"pill", "dots"}
	case "window_button_position", "appearance.window_button_position":
		return []string{"right", "left"}
	}
	return nil
}

// runGetLogs retrieves and displays daemon logs.
func runGetLogs(count int, clear bool, follow bool) error {
	if err := requireDaemon(); err != nil {
		return err
	}

	client := session.NewClient(&session.ClientConfig{
		Version: version,
	})

	if err := client.Connect(); err != nil {
		return explainDialError(err)
	}
	defer func() { _ = client.Close() }()

	if follow {
		// Follow mode: continuously poll for new logs
		return followLogs(client, count)
	}

	// Single retrieval
	_, err := displayLogs(client, count, clear)
	return err
}

// displayLogs fetches and displays logs once.
// displayLogs prints up to count entries and returns the newest timestamp shown
// (0 when none), which follow mode uses to seed its poll cursor.
func displayLogs(client *session.Client, count int, clear bool) (int64, error) {
	msg, err := session.NewMessage(session.MsgGetLogs, &session.GetLogsPayload{
		Count: count,
		Clear: clear,
	})
	if err != nil {
		return 0, fmt.Errorf("failed to create message: %w", err)
	}

	resp, err := client.SendControlMessage(msg)
	if err != nil {
		return 0, fmt.Errorf("failed to get logs: %w", err)
	}

	if resp.Type == session.MsgError {
		var errPayload session.ErrorPayload
		if err := resp.ParsePayloadWithCodec(&errPayload, client.GetCodec()); err != nil {
			return 0, fmt.Errorf("failed to get logs")
		}
		return 0, fmt.Errorf("failed to get logs: %s", errPayload.Message)
	}

	if resp.Type != session.MsgLogsData {
		return 0, fmt.Errorf("unexpected response type: %d", resp.Type)
	}

	var logsData session.LogsDataPayload
	if err := resp.ParsePayloadWithCodec(&logsData, client.GetCodec()); err != nil {
		return 0, fmt.Errorf("failed to parse logs: %w", err)
	}

	if len(logsData.Entries) == 0 {
		fmt.Println("No log entries")
		return 0, nil
	}

	var newest int64
	for _, entry := range logsData.Entries {
		ts := time.UnixMilli(entry.Timestamp)
		fmt.Printf("[%s] [%s] %s\n", ts.Format("15:04:05.000"), entry.Level, entry.Message)
		if entry.Timestamp > newest {
			newest = entry.Timestamp
		}
	}

	fmt.Printf("\n--- %d log entries ---\n", len(logsData.Entries))

	if clear {
		fmt.Println("(logs cleared)")
	}

	return newest, nil
}

// followLogs continuously polls for new logs.
func followLogs(client *session.Client, initialCount int) error {
	// First, display existing logs and seed the poll cursor from the newest one
	// shown, so the first tick does not reprint what we just displayed.
	lastTimestamp, err := displayLogs(client, initialCount, false)
	if err != nil {
		return err
	}

	fmt.Println("\n--- Following logs (Ctrl+C to stop) ---")

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	// Handle Ctrl+C
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)

	for {
		select {
		case <-sigChan:
			fmt.Println("\nStopped following logs")
			return nil
		case <-ticker.C:
			// Fetch new logs
			msg, err := session.NewMessage(session.MsgGetLogs, &session.GetLogsPayload{
				Count: 100, // Fetch last 100 entries to check for new ones
				Clear: false,
			})
			if err != nil {
				continue
			}

			resp, err := client.SendControlMessage(msg)
			if err != nil {
				continue
			}

			if resp.Type != session.MsgLogsData {
				continue
			}

			var logsData session.LogsDataPayload
			if err := resp.ParsePayloadWithCodec(&logsData, client.GetCodec()); err != nil {
				continue
			}

			// Display only new entries
			for _, entry := range logsData.Entries {
				if entry.Timestamp > lastTimestamp {
					ts := time.UnixMilli(entry.Timestamp)
					fmt.Printf("[%s] [%s] %s\n", ts.Format("15:04:05.000"), entry.Level, entry.Message)
					lastTimestamp = entry.Timestamp
				}
			}
		}
	}
}

// completeSessionNames returns available session names for shell completion.
func completeSessionNames(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	if !session.IsDaemonRunning() {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	client := session.NewClient(&session.ClientConfig{
		Version: version,
	})

	if err := client.Connect(); err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	defer func() { _ = client.Close() }()

	sessions, err := client.ListSessions()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	var names []string
	for _, s := range sessions {
		status := "detached"
		if s.Attached {
			status = "attached"
		}
		names = append(names, fmt.Sprintf("%s\t%s (%d windows)", s.Name, status, s.WindowCount))
	}

	return names, cobra.ShellCompDirectiveNoFileComp
}

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
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
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
