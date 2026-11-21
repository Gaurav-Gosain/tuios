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

func runListHooks(sessionName, event string, jsonOutput bool) error {

	params := map[string]any{"session": sessionName}
	if event != "" {
		params["event"] = event
	}
	raw, err := localVerb("list-hooks", params, 0, jsonOutput)
	if err != nil {
		return err
	}
	if jsonOutput {
		return printVerbResult(raw, jsonOutput)
	}
	return printHookList(raw)
}

func printHookList(raw json.RawMessage) error {
	var res struct {
		Hooks          []hookRow `json:"hooks"`
		Events         []string  `json:"events"`
		ClientAttached bool      `json:"client_attached"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
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
	fmt.Printf("\n%s.\n", plural.Count(len(res.Hooks), "hook"))
	if broken > 0 {
		fmt.Printf("%d failed. The LAST column carries the exit code and the error.\n", broken)
	}
	if !res.ClientAttached {
		fmt.Println("No client is attached, so the client-side hooks are not listed. " +
			"Attach the session to see after-attach, after-detach, after-resize and after-layout-change.")
	}
	return nil
}
