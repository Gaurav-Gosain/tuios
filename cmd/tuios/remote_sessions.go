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
	if res.HostFocus != "" && res.TUIAttached {
		fields = append(fields, [2]string{"host focus", res.HostFocus})
	}
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
	if len(res.WorkspaceOrder) > 0 {
		shown := make([]string, 0, len(res.WorkspaceOrder))
		for _, ws := range res.WorkspaceOrder {
			shown = append(shown, strconv.Itoa(ws))
		}
		fmt.Printf("%-14s %s\n", "order", strings.Join(shown, " "))
	}
	return nil
}

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

func runSetSessionSpace(sessionName, space string, global *bool) error {
	params := map[string]any{
		"session": sessionName,
		"space":   space,
	}
	if global != nil {
		params["global"] = *global
	}
	return callAndReport("set-session-space", params, func(res map[string]any) {
		if res["global"] == true {
			fmt.Printf("Session %v is global.\n", res["session"])
			return
		}
		if global != nil {
			fmt.Printf("Session %v is not global.\n", res["session"])
		}
		if res["space"] == "" {
			fmt.Printf("Session %v is in no space.\n", res["session"])
			return
		}
		fmt.Printf("Session %v is in space %v.\n", res["session"], res["space"])
	})
}

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
