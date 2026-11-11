package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/session"
	"github.com/spf13/cobra"
)

// querySession queries session info over the verb protocol (no TUI required).
func querySession(sessionName string, jsonOutput bool) error {
	t, err := dialSessionTarget(sessionName)
	if err != nil {
		return err
	}
	defer t.Close()

	raw, err := t.client.Call("session-info", t.params(map[string]any{"session": sessionName}))
	if err != nil {
		return reportVerbError(t.explain("session-info", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResultOn(t, raw, jsonOutput)
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
		HostFocus        string         `json:"host_focus"`
		WindowSize       string         `json:"window_size"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
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
	// Only a daemon that tracks it sends host_focus, and it says nothing
	// useful with no client attached.
	if res.HostFocus != "" && res.TUIAttached {
		fields = append(fields, [2]string{"host focus", res.HostFocus})
	}
	// Only a daemon that has the option sends window_size.
	if res.WindowSize != "" {
		fields = append(fields, [2]string{"window size", res.WindowSize})
	}
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
	// Only a daemon of a build that kept a display order beside the numbering
	// sends one. A current daemon renumbers the workspaces instead and always
	// sends null.
	if len(res.WorkspaceOrder) > 0 {
		shown := make([]string, 0, len(res.WorkspaceOrder))
		for _, ws := range res.WorkspaceOrder {
			shown = append(shown, strconv.Itoa(ws))
		}
		fmt.Printf("%-14s %s\n", "order", strings.Join(shown, " "))
	}
	return nil
}

// runRenameSession changes a session's name through the daemon. With no
// session given it renames the session of the pane it runs in, and never
// guesses one outside a pane.
func runRenameSession(target, name string) error {
	target = paneSessionOr(target)
	if target == "" {
		return errors.New("name the session to rename: tuios rename-session <session> <new-name>")
	}
	return callAndReport("rename-session", map[string]any{
		"session": target,
		"name":    name,
	}, func(res map[string]any) {
		fmt.Printf("Renamed session %v to %v.\n", res["old_name"], res["session"])
	})
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

// runSetSessionSpace files a session under a space, or takes it out of any
// when space is empty.
func runSetSessionSpace(sessionName, space string) error {
	return callAndReport("set-session-space", map[string]any{
		"session": sessionName,
		"space":   space,
	}, func(res map[string]any) {
		if res["space"] == "" {
			fmt.Printf("Session %v is in no space.\n", res["session"])
			return
		}
		fmt.Printf("Session %v is in space %v.\n", res["session"], res["space"])
	})
}

// runSetSessionOrder puts the daemon's sessions in an order every client
// lists, and prints the order that stands.
//
// The verb names no session, so it is called as it is, not through
// callAndReport, which adds one and so has the verb refuse the call.
func runSetSessionOrder(order []string) error {
	t, err := dialSessionTarget("")
	if err != nil {
		return err
	}
	defer t.Close()
	raw, err := t.client.Call("set-session-order", map[string]any{"order": order})
	if err != nil {
		return t.explain("set-session-order", err)
	}
	var res struct {
		Order []string `json:"order"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	fmt.Printf("Sessions in order: %s.\n", strings.Join(res.Order, ", "))
	return nil
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
