package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
)

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
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	fmt.Printf("workspace %d, %s\n", res.Current, plural.Count(res.WindowCount, "window"))
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
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
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
	fmt.Printf("\n%s. * marks the one showing.\n", plural.Count(len(res.Workspaces), "workspace"))
	return nil
}

// checkSetLayoutMaster refuses a master position or count set-layout cannot
// take, before anything is sent.
func checkSetLayoutMaster(position string, masters int, mastersSet bool) error {
	if position != "" && !config.IsMasterPosition(position) {
		return errfmt.Errorf("--master-position takes %s, got %q", strings.Join(config.MasterPositions, ", "), position)
	}
	if mastersSet && !config.ValidMasterCount(masters) {
		return errfmt.Errorf("--masters takes a number from %d to %d, got %d", config.MasterCountMin, config.MasterCountMax, masters)
	}
	return nil
}

// masterCountNames is the accepted master counts, as completions.
func masterCountNames() []string {
	names := make([]string, 0, config.MasterCountMax-config.MasterCountMin+1)
	for n := config.MasterCountMin; n <= config.MasterCountMax; n++ {
		names = append(names, strconv.Itoa(n))
	}
	return names
}

// runSetLayout turns tiling on or off and tidies the splits. tiling is a
// pointer so a call that only equalizes leaves the tiling mode alone.
func runSetLayout(sessionName string, tiling *bool, equalize, rotate bool, masterPosition string, masters int, jsonOutput bool) error {
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
	if masterPosition != "" {
		params["master_position"] = masterPosition
	}
	if masters != 0 {
		params["master_count"] = masters
	}

	raw, err := client.Call("set-layout", params)
	if err != nil {
		return reportVerbError(explainVerbError("set-layout", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, jsonOutput)
	}
	var res struct {
		TilingMode     string  `json:"tiling_mode"`
		LayoutMode     string  `json:"layout_mode"`
		MasterRatio    float64 `json:"master_ratio"`
		MasterPosition string  `json:"master_position"`
		MasterCount    int     `json:"master_count"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	fmt.Printf("%s  layout %s  master %.2f", res.TilingMode, res.LayoutMode, res.MasterRatio)
	if res.MasterCount > 0 {
		fmt.Printf("  %s  masters %d", res.MasterPosition, res.MasterCount)
	}
	fmt.Println()
	return nil
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
