package app

import (
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// The rail's custom section: the rows a command of the user's printed.
//
// The command is a dock component in everything but where it draws. It is
// scheduled by the dock engine, with the dock's refresh grammar, debounce,
// sanitiser, timeout and failure rule, because a second scheduler for the
// same kind of subprocess would be a second set of ways for it to go wrong.
// Three things differ, and each is a flag or a field on the component rather
// than a path of its own: every line is a row (MultiLine), an event that
// lands mid-run keeps one re-run (Coalesce), and the run's environment
// carries the focus and the section's size, which the model hands to the
// engine under its lock because runs start off the render goroutine.

// railCustomComponent is the engine name of the section's command. It is not
// under the dock's custom/ prefix, so it is never drawn as a bar cell, and
// tuios refresh-dock rail/custom reaches it.
const railCustomComponent = "rail/custom"

// railCustomState is the model's side of the section.
type railCustomState struct {
	// on records whether the engine was built with the section's command,
	// so the per-message sync can tell a layout change from a frame.
	on bool
	// gen counts the updates that changed the rows. The render cache folds
	// it, so new output redraws the rail and an unchanged value does not.
	gen uint64
}

// railCustomEnabled reports whether the layout names the section. The
// section draws its title from this alone: naming it with no command set
// draws the title over an empty section, as the spec says, so a person who
// placed it sees where it went.
func (m *OS) railCustomEnabled() bool {
	return sidebarLayoutHas(sidebarSectionCustom, &m.Settings)
}

// railCustomConfig is the section's table, or the zero table for a model
// with no config loaded.
func (m *OS) railCustomConfig() config.SidebarCustomConfig {
	if m.UserConfig == nil {
		return config.SidebarCustomConfig{}
	}
	return m.UserConfig.Appearance.Sidebar.Custom
}

// railCustomTitle is the section's heading.
func (m *OS) railCustomTitle() string {
	return m.railCustomConfig().ResolvedTitle()
}

// railCustomRows is what the section draws, one row per line the command
// printed. Nothing yet: no engine feeds the section.
func (m *OS) railCustomRows() []string {
	return nil
}

// sidebarCustomRow draws one row: the command's own text, colour included,
// cut to the rail's columns and closed with a reset so a colour the command
// left open cannot run into the padding or the row below.
func (m *OS) sidebarCustomRow(text string, cw int, pal overlay.Palette, st sidebarRowState) string {
	rowBg := sidebarRowBg(st, pal)
	body := overlay.Truncate(strings.TrimRight(text, " "), max(cw-1, 0)) + "\x1b[0m"
	return sidebarFit(sidebarStyle(rowBg, nil).Render(" ")+body, cw, rowBg)
}
