package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
)

// tuios list-hooks is the dock's debugging story, applied to the extension
// point that never had one.
//
// list-dock-components answers "why does my dock component print nothing"
// because it reports the exit code, the last run and the last error. A hook ran
// with its output discarded and its error dropped, so the same question about a
// hook had no answer at all. This prints the same three columns.

// hookRow is one row of the list-hooks result.
type hookRow struct {
	Event    string `json:"event"`
	Side     string `json:"side"`
	Command  string `json:"command"`
	Runs     int    `json:"runs"`
	LastExit int    `json:"last_exit"`
	LastRun  string `json:"last_run"`
	LastErr  string `json:"last_error"`
	LastMs   int64  `json:"last_ms"`
}

// runListHooks prints the hook table and what each command last did.
func runListHooks(sessionName, event string, jsonOutput bool) error {
	client, err := dialVerb()
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	params := map[string]any{"session": sessionName}
	if event != "" {
		params["event"] = event
	}
	raw, err := client.Call("list-hooks", params)
	if err != nil {
		return reportVerbError(explainVerbError("list-hooks", err), jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, jsonOutput)
	}
	return printHookList(raw)
}

// printHookList renders the listing as a table.
func printHookList(raw json.RawMessage) error {
	var res struct {
		Hooks          []hookRow `json:"hooks"`
		Events         []string  `json:"events"`
		ClientAttached bool      `json:"client_attached"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	if len(res.Hooks) == 0 {
		fmt.Println("No hooks are registered. Add a [hooks] table to your config.")
		fmt.Printf("Events: %s\n", strings.Join(res.Events, ", "))
		return nil
	}

	rows := make([][]string, 0, len(res.Hooks))
	broken := 0
	for _, h := range res.Hooks {
		state := "waiting"
		switch {
		case h.LastErr != "":
			state, broken = "failed", broken+1
		case h.Runs > 0:
			state = "ran"
		}
		detail := h.LastRun
		if h.LastErr != "" {
			detail = fmt.Sprintf("exit %d: %s", h.LastExit, h.LastErr)
		}
		rows = append(rows, []string{
			h.Event, h.Side, trimCell(h.Command, 40), fmt.Sprint(h.Runs), state, trimCell(detail, 40),
		})
	}

	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("8"))).
		Headers("EVENT", "SIDE", "COMMAND", "RUNS", "STATE", "LAST").
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
	fmt.Printf("\n%d hook(s).\n", len(res.Hooks))
	if broken > 0 {
		fmt.Printf("%d failed. The LAST column carries the exit code and the error.\n", broken)
	}
	if !res.ClientAttached {
		fmt.Println("No client is attached, so the client-side hooks are not listed. " +
			"Attach the session to see after-attach, after-detach, after-resize and after-layout-change.")
	}
	return nil
}
