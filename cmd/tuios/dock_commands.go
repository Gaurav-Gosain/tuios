package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
)

type dockComponentRow struct {
	Name     string `json:"name"`
	Side     string `json:"side"`
	Source   string `json:"source"`
	Refresh  string `json:"refresh"`
	Interval string `json:"interval"`
	Events   string `json:"events"`
	Command  string `json:"command"`
	Text     string `json:"text"`
	Visible  bool   `json:"visible"`
	Off      string `json:"off"`
	LastExit int    `json:"last_exit"`
	LastRun  string `json:"last_run"`
	LastErr  string `json:"last_error"`
	Stopped  bool   `json:"stopped"`
}

func queryDockComponents(sessionName string, jsonOutput bool) error {
	raw, err := localVerb("list-dock-components", map[string]any{"session": sessionName}, 0, jsonOutput)
	if err != nil {
		return err
	}
	if jsonOutput {
		return printVerbResult(raw, jsonOutput)
	}
	return printDockComponentList(raw)
}

func runRefreshDock(sessionName, component string, jsonOutput bool) error {

	params := map[string]any{"session": sessionName}
	if component != "" {
		params["component"] = component
	}
	raw, err := localVerb("refresh-dock", params, 0, jsonOutput)
	if err != nil {
		return err
	}
	if jsonOutput {
		return printVerbResult(raw, jsonOutput)
	}
	var res struct {
		Component string `json:"component"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	if res.Component == "all" {
		fmt.Println("Refreshed every dock component.")
		return nil
	}
	fmt.Printf("Refreshed dock component %q.\n", res.Component)
	return nil
}

func printDockComponentList(raw json.RawMessage) error {
	var res struct {
		Components []dockComponentRow `json:"components"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	if len(res.Components) == 0 {
		fmt.Println("The dock draws no components. Check the [dock] table in your config.")
		return nil
	}

	rows := make([][]string, 0, len(res.Components))
	broken := 0
	for _, c := range res.Components {
		refresh := c.Refresh
		switch {
		case c.Interval != "":
			refresh += " " + c.Interval
		case c.Events != "":
			refresh += ":" + c.Events
		}
		state := "drawn"
		switch {
		case c.Stopped:
			state, broken = "gave up", broken+1
		case c.LastErr != "":
			state, broken = "failed", broken+1
		case c.Off != "":
			state = "off"
		case !c.Visible:
			state = "hidden"
		}
		detail := c.Text
		if c.Off != "" {
			detail = c.Off
		}
		if c.LastErr != "" {
			detail = c.LastErr
			if c.LastExit != 0 {
				detail = fmt.Sprintf("exit %d: %s", c.LastExit, c.LastErr)
			}
		}
		rows = append(rows, []string{
			c.Name, c.Side, c.Source, refresh, state, trimCell(detail, 40),
		})
	}

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("8"))).
		Headers("COMPONENT", "SIDE", "SOURCE", "REFRESH", "STATE", "READS").
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			base := lipgloss.NewStyle().Padding(0, 1)
			if row == table.HeaderRow {
				return base.Bold(true).Foreground(lipgloss.Color("12"))
			}
			switch col {
			case 0:
				return base.Foreground(lipgloss.Color("3")).Bold(true)
			case 1, 2, 3:
				return base.Foreground(lipgloss.Color("8"))
			default:
				return base
			}
		})

	fmt.Println(t.Render())
	fmt.Printf("\n%s.\n", plural.Count(len(res.Components), "component"))
	if broken > 0 {
		fmt.Printf("%d is not drawing; the READS column carries the reason. "+
			"Fix the script and run 'tuios refresh-dock <name>'.\n", broken)
	}
	return nil
}

func trimCell(s string, width int) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\n", " "), "\r", "")
	if lipgloss.Width(s) <= width {
		return s
	}
	runes := []rune(s)
	if len(runes) > width-1 {
		runes = runes[:width-1]
	}
	return string(runes) + "…"
}
