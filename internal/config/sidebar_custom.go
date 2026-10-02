package config

import (
	"fmt"
	"strings"
)

// The rail's custom section: a section whose rows are what a command of the
// user's prints. It borrows the dock's component contract (environment in,
// text out, the same refresh grammar, the same sanitiser and the same failure
// rule) and differs where a rail section is not a bar cell: every line of
// stdout is a row, and there is no push mode, because a command that stays
// running keeps the environment it started with and cannot see the focus or
// the section's size change.

// SidebarCustomConfig is [appearance.sidebar.custom].
//
// It stays out of the option registry on purpose, so set-option and
// set-config cannot set the command. The command runs outside every pane on
// every refresh, as dock commands and hooks do, and in the default open mode
// any pane can call set-option. The section's place in the layout stays
// settable; only what it runs is not. daemon.respond_from_shell is kept out
// of the registry for the same reason.
type SidebarCustomConfig struct {
	// Title is the section's heading. Empty means SidebarCustomDefaultTitle.
	Title string `toml:"title,omitempty"`
	// Command is run through sh -c. Each line of its stdout is a row.
	Command string `toml:"command,omitempty"`
	// Refresh is when to run it: "once" (the default), a duration such as
	// "30s", or "event:TYPE[,TYPE]". "push" is refused.
	Refresh string `toml:"refresh,omitempty"`
}

// SidebarCustomDefaultTitle is the heading the section draws when the table
// names none.
const SidebarCustomDefaultTitle = "Custom"

// ResolvedTitle is the heading the section draws.
func (c SidebarCustomConfig) ResolvedTitle() string {
	if t := strings.TrimSpace(c.Title); t != "" {
		return t
	}
	return SidebarCustomDefaultTitle
}

// HasCommand reports whether the table names a command to run.
func (c SidebarCustomConfig) HasCommand() bool {
	return strings.TrimSpace(c.Command) != ""
}

// ParseSidebarCustomRefresh reads the section's refresh field. It is the
// dock's grammar with push taken out.
func ParseSidebarCustomRefresh(s string) (DockRefresh, error) {
	refresh, err := ParseDockRefresh(s)
	if err != nil {
		return DockRefresh{}, err
	}
	if refresh.Kind == DockRefreshPush {
		return DockRefresh{}, fmt.Errorf("refresh %q is refused for the rail section: "+
			"a command that stays running cannot see the focus or the section's size change; "+
			"use once, a duration or event:TYPE", strings.TrimSpace(s))
	}
	return refresh, nil
}

// SidebarCustomPlaced reports whether the layout names the custom section.
func SidebarCustomPlaced(sections string) bool {
	for _, e := range ParseSidebarSections(sections) {
		if e.Name == SidebarSectionCustom {
			return true
		}
	}
	return false
}

// validateSidebarCustom appends the custom section's problems to result, as
// warnings, the way validateDock does for dock components: a section that
// draws nothing and says nothing about why is the failure mode the dock's
// warnings exist to stop.
func validateSidebarCustom(cfg *UserConfig, result *ValidationResult) {
	custom := cfg.Appearance.Sidebar.Custom
	placed := SidebarCustomPlaced(cfg.Appearance.Sidebar.Sections)
	warn := func(key, message string) {
		result.Warnings = append(result.Warnings, ValidationError{
			Field: "appearance.sidebar.custom", Key: key, Message: message,
		})
	}
	if placed && !custom.HasCommand() {
		warn("command", "the layout names the custom section but no command is set, so it draws its title over nothing")
	}
	if !placed && custom.HasCommand() {
		warn("command", `a command is set but "custom" is not in appearance.sidebar.sections, so it never runs`)
	}
	refresh, err := ParseSidebarCustomRefresh(custom.Refresh)
	switch {
	case err != nil:
		warn("refresh", err.Error()+"; the section runs nothing")
	case refresh.Kind == DockRefreshEvent:
		for _, event := range refresh.Events {
			if IsDockEventType(event) {
				continue
			}
			warn("refresh", fmt.Sprintf("nothing ever fires %q, so the section would never refresh; the events are %s",
				event, strings.Join(DockEventTypes(), ", ")))
		}
	}
}
