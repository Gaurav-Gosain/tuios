package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/plural"
	"github.com/Gaurav-Gosain/tuios/internal/sortx"
)

func runListOptions(sessionName, section, prefix, search string, jsonOutput bool) error {
	raw, err := localVerb("list-options", map[string]any{
		"session": sessionName,
		"section": section,
		"prefix":  prefix,
	}, 0, jsonOutput)
	if err != nil {
		return err
	}
	if search != "" {
		return printOptionSearch(os.Stdout, raw, search, jsonOutput)
	}
	if jsonOutput {
		return printVerbResult(raw, jsonOutput)
	}
	var res struct {
		Options  []optionRow `json:"options"`
		Sections []string    `json:"sections"`
		Total    int         `json:"total"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	printOptionList(os.Stdout, res.Options, res.Sections, res.Total)
	return nil
}

func printOptionList(w io.Writer, options []optionRow, sections []string, total int) {
	if len(options) == 0 {
		fmt.Fprintln(w, "No options match that filter.")
		return
	}

	options = slices.Clone(options)
	sortx.SortFunc(options, func(a, b optionRow) int {
		if c := strings.Compare(a.Section, b.Section); c != 0 {
			return c
		}
		return strings.Compare(a.Path, b.Path)
	})

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
		fmt.Fprintf(w, "  %-*s  %-6s  default %s%s\n", width, opt.Path, opt.Type, withUnit(orNone(opt.Default), opt.Unit), deprecated)
		fmt.Fprintf(w, "  %-*s  %s\n", width, "", opt.Description)
		if len(opt.Accepted) > 0 {
			fmt.Fprintf(w, "  %-*s  one of: %s\n", width, "", strings.Join(opt.Accepted, ", "))
		}
		if opt.Max > 0 {
			auto := ""
			if opt.Auto {
				auto = ", or auto"
			}
			fmt.Fprintf(w, "  %-*s  range: %d to %s%s\n", width, "", opt.Min, withUnit(strconv.Itoa(opt.Max), opt.Unit), auto)
		}
		if opt.SessionVal != "" {
			fmt.Fprintf(w, "  %-*s  this session: %s\n", width, "", opt.SessionVal)
		}
		if opt.Deprecated != "" {
			fmt.Fprintf(w, "  %-*s  %s\n", width, "", opt.Deprecated)
		}
	}

	fmt.Fprintf(w, "\n%s. Set one with 'tuios set-config <path> <value>'.\n", plural.Count(total, "option"))
	if len(sections) > 0 {
		fmt.Fprintf(w, "Sections: %s\n", strings.Join(sections, ", "))
		fmt.Fprintln(w, "Narrow with --section <name>, or pass a path prefix as the argument.")
	}
}

func runSetConfig(sessionName, path, value string, jsonOutput, noSave bool) error {
	raw, err := setConfigOptionWith(sessionName, path, value, noSave)
	if err != nil {
		return err
	}
	if noSave && !daemonHonoursNoSave(raw) {
		return &diagnosticError{
			What:  "The value is set, but the daemon saved it to the config file.",
			Cause: "the daemon is older than this tuios and does not know --no-save.",
			Extra: []string{"Upgrade the daemon before you use --no-save again."},
			Fix:   "tuios daemon upgrade",
		}
	}
	if jsonOutput {
		var pretty any
		if err := jsonx.Unmarshal(raw, &pretty); err != nil {
			return errfmt.Errorf("failed to parse response: %w", err)
		}
		return printJSON(pretty)
	}
	fmt.Printf("Set %s = %s\n", path, value)
	if note := setConfigNote(raw); note != "" {
		fmt.Fprintln(os.Stderr, note)
	}
	return nil
}

func daemonHonoursNoSave(raw json.RawMessage) bool {
	var res struct {
		NoSave *bool `json:"no_save"`
	}
	return jsonx.Unmarshal(raw, &res) == nil && res.NoSave != nil && *res.NoSave
}

func setConfigNote(raw json.RawMessage) string {
	var res struct {
		Applied    bool   `json:"applied"`
		Reason     string `json:"reason"`
		Deprecated string `json:"deprecated"`
	}
	if jsonx.Unmarshal(raw, &res) != nil {
		return ""
	}
	var lines []string
	if !res.Applied && res.Reason != "" {
		lines = append(lines, "Not applied: "+strings.TrimSuffix(res.Reason, ".")+".")
	}
	if res.Deprecated != "" {
		lines = append(lines, "Deprecated: "+res.Deprecated)
	}
	return strings.Join(lines, "\n")
}

func setConfigOption(sessionName, path, value string) (json.RawMessage, error) {
	return setConfigOptionWith(sessionName, path, value, false)
}

func setConfigOptionWith(sessionName, path, value string, noSave bool) (json.RawMessage, error) {

	params := map[string]any{
		"session": sessionName,
		"key":     path,
		"value":   value,
	}
	if noSave {
		params["no_save"] = true
	}
	raw, err := localVerb("set-option", params, 0, false)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

func runGetConfig(sessionName, path string, jsonOutput bool) error {
	raw, err := localVerb("get-option", map[string]any{
		"session": sessionName,
		"key":     path,
	}, 0, false)
	if err != nil {
		return err
	}
	if jsonOutput {
		var pretty any
		if err := jsonx.Unmarshal(raw, &pretty); err != nil {
			return errfmt.Errorf("failed to parse response: %w", err)
		}
		out, err := json.MarshalIndent(pretty, "", "  ")
		if err != nil {
			return errfmt.Errorf("failed to render response: %w", err)
		}
		fmt.Println(string(out))
		return nil
	}
	var res struct {
		Value string `json:"value"`
	}
	if err := jsonx.Unmarshal(raw, &res); err != nil {
		return errfmt.Errorf("failed to parse response: %w", err)
	}
	fmt.Println(getConfigText(path, res.Value))
	return nil
}

func getConfigText(path, value string) string {
	if value == "" {
		if o, ok := config.LookupOption(path); ok && o.Follows != "" {
			return o.UnsetText()
		}
	}
	return value
}

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

func getConfigValueCompletions(path, _ string) []string {
	switch path {
	case "dockbar_position", "appearance.dockbar_position":
		return []string{"top", "bottom", "hidden"}
	case "border_style", "appearance.border_style":
		return []string{"rounded", "normal", "thick", "double", "hidden", "block", "ascii"}
	case "animations", "appearance.animations_enabled", "animations_enabled":
		return []string{"true", "false", "toggle", "on", "off"}
	case "motion", "appearance.motion":
		return config.MotionLevels
	case "hide_window_buttons", "appearance.hide_window_buttons":
		return []string{"true", "false"}
	case "window_button_style", "appearance.window_button_style":
		return []string{"pill", "dots"}
	case "window_button_position", "appearance.window_button_position":
		return []string{"right", "left"}
	}
	return nil
}

func withUnit(text, unit string) string {
	return config.Option{Unit: unit}.WithUnit(text)
}
