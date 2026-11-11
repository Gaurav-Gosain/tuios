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

// runListOptions lists the settable configuration paths. It is the command that
// answers "what can I set", so the human form prints the whole contract of each
// option rather than a bare path a caller would still have to look up.
func runListOptions(sessionName, section, prefix, search string, jsonOutput bool) error {
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

// printOptionList writes the options grouped by section, then the section names
// so a reader who found the list too long can narrow it without a second guess.
func printOptionList(w io.Writer, options []optionRow, sections []string, total int) {
	if len(options) == 0 {
		fmt.Fprintln(w, "No options match that filter.")
		return
	}

	// A section is a display group, not a path prefix, so regroup a clone
	// rather than reprint a heading each time path order crosses back into one.
	options = slices.Clone(options)
	sortx.SortFunc(options, func(a, b optionRow) int {
		if c := strings.Compare(a.Section, b.Section); c != 0 {
			return c
		}
		return strings.Compare(a.Path, b.Path)
	})

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

// runSetConfig sets a session option over the verb protocol. The value is
// recorded in daemon-owned state and, when a TUI is attached, applied live.
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
	// The line on stdout stays as it was, for scripts. What the daemon says
	// about a value it only recorded goes to stderr, so a person sees why the
	// screen did not change.
	fmt.Printf("Set %s = %s\n", path, value)
	if note := setConfigNote(raw); note != "" {
		fmt.Fprintln(os.Stderr, note)
	}
	return nil
}

// daemonHonoursNoSave reports whether the set-option answer says the daemon
// used no_save. A daemon that predates the field leaves it out and saves the
// value.
func daemonHonoursNoSave(raw json.RawMessage) bool {
	var res struct {
		NoSave *bool `json:"no_save"`
	}
	return jsonx.Unmarshal(raw, &res) == nil && res.NoSave != nil && *res.NoSave
}

// setConfigNote is what set-config says on stderr about the daemon's answer:
// a value only recorded, and a deprecated path. It is empty when there is
// nothing to say.
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

// setConfigOption is the set-option call behind set-config, and returns the
// daemon's answer. tuios config browse sets a value through it too, so the
// explorer has the same refusals as the command.
func setConfigOption(sessionName, path, value string) (json.RawMessage, error) {
	return setConfigOptionWith(sessionName, path, value, false)
}

// setConfigOptionWith is setConfigOption, with noSave for set-config --no-save.
func setConfigOptionWith(sessionName, path, value string, noSave bool) (json.RawMessage, error) {
	client, err := dialVerb()
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close() }()

	params := map[string]any{
		"session": sessionName,
		"key":     path,
		"value":   value,
	}
	// Sent only when set, so the call a plain set-config makes is the one an
	// older daemon has always taken.
	if noSave {
		params["no_save"] = true
	}
	raw, err := client.Call("set-option", params)
	if err != nil {
		return nil, explainVerbError("set-option", err)
	}
	return raw, nil
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

// getConfigText is what get-config prints for value at path. An unset option
// that follows another says so, since an empty line reads as no value at all.
func getConfigText(path, value string) string {
	if value == "" {
		if o, ok := config.LookupOption(path); ok && o.Follows != "" {
			return o.UnsetText()
		}
	}
	return value
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

// withUnit is a number from list-options followed by its unit, as the
// settings page shows it: "2000 ms", "45%". Anything else is returned as it is.
func withUnit(text, unit string) string {
	return config.Option{Unit: unit}.WithUnit(text)
}
