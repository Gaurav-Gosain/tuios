package config

import "fmt"

// SidebarEdgeConfig holds the file-only overrides for one screen edge under
// [appearance.sidebar.left] or [appearance.sidebar.right]. The original
// [appearance.sidebar] remains the configuration of the original rail. Its
// edge's overrides take precedence; a different edge starts disabled until
// its own enabled field is set. Commands are not settable options: a pane must
// not be able to replace a command that runs in the client outside the pane.
type SidebarEdgeConfig struct {
	Enabled  *bool                `toml:"enabled,omitempty"`
	Width    int                  `toml:"width,omitempty"`
	Sections string               `toml:"sections,omitempty"`
	Custom   *SidebarCustomConfig `toml:"custom,omitempty"`
}

// SidebarEdgeResolved is the effective rail settings on one side of the
// screen. It deliberately contains only the settings which vary by edge;
// appearance settings such as row glyphs still come from the shared palette.
type SidebarEdgeResolved struct {
	Enabled  bool
	Width    int
	Sections string
	Custom   SidebarCustomConfig
}

// ResolveSidebarEdges merges the legacy rail with explicit per-edge
// overrides. A config with no left/right tables resolves to the one existing
// rail, at the legacy position. Setting the other edge's enabled flag opts
// into a second rail; simply writing its custom command does not start one.
func ResolveSidebarEdges(sidebar SidebarConfig) (left, right SidebarEdgeResolved) {
	primary := SidebarEdgeResolved{
		Enabled:  sidebar.Enabled == nil || *sidebar.Enabled,
		Width:    sidebar.Width,
		Sections: sidebar.Sections,
		Custom:   sidebar.Custom,
	}
	if primary.Width <= 0 {
		primary.Width = SidebarDefaultWidth
	}
	if primary.Sections == "" {
		primary.Sections = SidebarDefaultSections
	}
	switch sidebar.Position {
	case "hidden":
		// A hidden legacy rail does not prevent explicit per-edge rails.
	case "left":
		left = primary
	default:
		right = primary
	}
	apply := func(dst *SidebarEdgeResolved, cfg *SidebarEdgeConfig) {
		if cfg == nil {
			return
		}
		if cfg.Enabled != nil {
			dst.Enabled = *cfg.Enabled
		}
		if cfg.Width > 0 {
			dst.Width = cfg.Width
		}
		if cfg.Sections != "" {
			dst.Sections = cfg.Sections
		}
		if cfg.Custom != nil {
			dst.Custom = *cfg.Custom
		}
		if dst.Width <= 0 {
			dst.Width = SidebarDefaultWidth
		}
		if dst.Sections == "" {
			dst.Sections = SidebarDefaultSections
		}
	}
	apply(&left, sidebar.Left)
	apply(&right, sidebar.Right)
	return left, right
}

// validateSidebarEdges checks explicitly configured edges. Their custom
// commands have the same security and refresh contract as the legacy rail.
func validateSidebarEdges(sidebar SidebarConfig, result *ValidationResult) {
	left, right := ResolveSidebarEdges(sidebar)
	for _, edge := range []struct {
		name     string
		cfg      *SidebarEdgeConfig
		resolved SidebarEdgeResolved
	}{
		{"left", sidebar.Left, left},
		{"right", sidebar.Right, right},
	} {
		if edge.cfg == nil {
			continue
		}
		field := "appearance.sidebar." + edge.name
		warn := func(key, message string) {
			result.Warnings = append(result.Warnings, ValidationError{
				Field: field, Key: key, Message: message,
			})
		}
		if edge.cfg.Width < 0 {
			warn("width", "a negative width is not usable; the default width is used")
		}
		for _, problem := range SidebarSectionProblems(edge.cfg.Sections) {
			warn("sections", problem)
		}
		if edge.cfg.Custom == nil {
			continue
		}
		custom := *edge.cfg.Custom
		placed := SidebarCustomPlaced(edge.resolved.Sections)
		if edge.resolved.Enabled && placed && !custom.HasCommand() {
			warn("custom.command", "the layout names custom but no command is set")
		}
		if custom.HasCommand() && (!edge.resolved.Enabled || !placed) {
			warn("custom.command", "the command cannot run unless this edge is enabled and its layout names custom")
		}
		if _, err := ParseSidebarCustomRefresh(custom.Refresh); err != nil {
			warn("custom.refresh", fmt.Sprintf("%v; the section runs nothing", err))
		}
	}
}
