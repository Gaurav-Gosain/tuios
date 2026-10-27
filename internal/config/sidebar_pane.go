package config

import (
	"strconv"
	"strings"
)

// The rail's pane section: a live terminal pane drawn in the rail, under the
// heading the table gives it. Its program runs in a real PTY in the session,
// so the daemon owns it and it survives a detach. See app/rail_pane.go for
// where the pane lives and how input reaches it.

// SidebarSectionPane is the rail section that draws a live pane, configured
// in [appearance.sidebar.pane].
const SidebarSectionPane = "pane"

// SidebarPaneDefaultTitle is the heading the section draws when the table
// names none.
const SidebarPaneDefaultTitle = "pane"

// SidebarPaneMinCols is the narrowest rail content the pane is drawn in. A
// program squeezed below this draws nothing worth reading, so the section is
// left out and the program keeps its last size.
const SidebarPaneMinCols = 10

// SidebarPaneConfig is [appearance.sidebar.pane].
//
// It stays out of the option registry for the reason SidebarCustomConfig
// does: the command runs outside every pane, and set-option is open to any
// pane in the default mode.
type SidebarPaneConfig struct {
	// Title is the section's heading. Empty means SidebarPaneDefaultTitle.
	Title string `toml:"title,omitempty"`
	// Command is the program the pane runs, through sh -c (cmd /C on
	// Windows). The command line is also what says two clients mean the same
	// pane: a different line replaces the pane.
	Command string `toml:"command,omitempty"`
	// Height is the most rows the pane asks for. Zero takes what the
	// section's share in appearance.sidebar.sections gives it.
	Height int `toml:"height,omitempty"`
}

// ResolvedTitle is the heading the section draws.
func (c SidebarPaneConfig) ResolvedTitle() string {
	if t := strings.TrimSpace(c.Title); t != "" {
		return t
	}
	return SidebarPaneDefaultTitle
}

// CommandLine is the command as the pane runs it, trimmed. Empty means no
// pane.
func (c SidebarPaneConfig) CommandLine() string {
	return strings.TrimSpace(c.Command)
}

// SidebarPanePlaced reports whether the layout names the pane section.
func SidebarPanePlaced(sections string) bool {
	for _, e := range ParseSidebarSections(sections) {
		if e.Name == SidebarSectionPane {
			return true
		}
	}
	return false
}

// validateSidebarPane appends the pane section's problems to result as
// warnings, the way validateSidebarCustom does.
func validateSidebarPane(cfg *UserConfig, result *ValidationResult) {
	sidebar := cfg.Appearance.Sidebar
	placed := SidebarPanePlaced(sidebar.Sections)
	for _, edge := range []*SidebarEdgeConfig{sidebar.Left, sidebar.Right} {
		if edge != nil && SidebarPanePlaced(edge.Sections) {
			placed = true
		}
	}
	pane := sidebar.Pane
	warn := func(key, message string) {
		result.Warnings = append(result.Warnings, ValidationError{
			Field: "appearance.sidebar.pane", Key: key, Message: message,
		})
	}
	if placed && pane.CommandLine() == "" {
		warn("command", "the layout names the pane section but no command is set, so the section is not drawn")
	}
	if !placed && pane.CommandLine() != "" {
		warn("command", `a command is set but "pane" is not in appearance.sidebar.sections, so it never runs`)
	}
	if pane.Height < 0 {
		warn("height", "height is a number of rows and cannot be negative. Set 0 to take the section's share")
	}
	if pane.Height > 0 && pane.Height < 2 {
		warn("height", "a pane of "+strconv.Itoa(pane.Height)+" row is too short to show a program. Use 2 or more")
	}
}
