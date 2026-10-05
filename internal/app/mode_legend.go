package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

// The mode legend: what the keys do in the mode the keyboard is in.
//
// One rule for every mode that takes the keyboard over with keys of its own
// (copy mode, multi copy mode, hints mode). Its keys are a legend in the
// dock's right-hand block, for as long as the mode is open, and never a
// message. A message burns down and goes while the mode stays, it queues
// behind other messages, and it is prose: the dock cut "Type a label to copy.
// Shift+label types it. Ctrl+label opens it" in the middle of a word, so the
// keys after the cut were never seen. The prefix menus have the same need and
// meet it with the which-key panel.
//
// A legend is one list of key and label pairs, the most important first, each
// with a priority. The dock fits it with the strip every panel footer uses
// (overlay.FitHintStrip): modifier names shortened first, then whole pairs
// dropped, the optional ones first and within a priority the last listed
// first. So a narrow screen shows fewer keys, never part of one. Three things
// stay at any width the dock has: the main action, the way out of the mode,
// which is always esc or names it, and in hints mode the key that lists the
// rest. No legend moves or scrolls: text that moves cannot be read at a
// glance, and a frame that is redrawn to move it costs an idle session its
// sleep.

// modeLegend is the legend of the mode the keyboard is in, or nil when the
// keyboard is in no mode with keys of its own.
func (m *OS) modeLegend() []overlay.Hint {
	if m.hints != nil {
		return m.hintsLegend()
	}
	if m.paneLabels != nil {
		return paneLabelsLegend()
	}
	if fw := m.GetFocusedWindow(); fw.CopyModeVisible() {
		return m.copyModeHelp(fw)
	}
	return nil
}

// hintsLegend is hints mode's legend. Ctrl and a label is left out when the
// config turns opening off, since it only copies then.
func (m *OS) hintsLegend() []overlay.Hint {
	legend := []overlay.Hint{
		{Key: "label", Label: "copy", Priority: overlay.HintEssential},
		{Key: "shift+label", Label: "type"},
	}
	if m.hintsConfig().OpenEnabled() {
		legend = append(legend, overlay.Hint{Key: "ctrl+label", Label: "open"})
	}
	return append(legend,
		overlay.Hint{Key: hintsHelpKey, Label: "help", Priority: overlay.HintEssential},
		overlay.Hint{Key: "esc", Label: "cancel"},
	)
}

// paneLabelsLegend is the pane labels' legend.
func paneLabelsLegend() []overlay.Hint {
	return []overlay.Hint{
		{Key: "label", Label: "focus pane", Priority: overlay.HintEssential},
		{Key: "backspace", Label: "undo key", Priority: overlay.HintOptional},
		{Key: "esc", Label: "cancel", Priority: overlay.HintEssential},
	}
}

// copyModeLegend is copy mode's legend for a sub-state.
//
// They are key/label pairs rather than a "hjkl:move" string because the dock
// is the one place left saying what a key does in its own format. Rendered
// through the strip a panel footer uses, copy mode reads like the rest of the
// app.
func copyModeLegend(state terminal.CopyModeState) []overlay.Hint {
	switch state {
	case terminal.CopyModeNormal:
		return []overlay.Hint{
			{Key: "hjkl", Label: "move"},
			{Key: "w/b/e", Label: "word", Priority: overlay.HintOptional},
			{Key: "f/t", Label: "char", Priority: overlay.HintOptional},
			{Key: "/", Label: "search"},
			{Key: "n/N", Label: "next", Priority: overlay.HintOptional},
			{Key: "v", Label: "visual"},
			{Key: "y", Label: "yank", Priority: overlay.HintEssential},
			{Key: "q/esc", Label: "quit", Priority: overlay.HintEssential},
		}
	case terminal.CopyModeSearch:
		return []overlay.Hint{
			{Key: "type", Label: "search", Priority: overlay.HintOptional},
			{Key: "n/N", Label: "next"},
			{Key: overlay.EnterKey(), Label: "done", Priority: overlay.HintEssential},
			{Key: "esc", Label: "cancel"},
		}
	case terminal.CopyModeVisualChar:
		return []overlay.Hint{
			{Key: "hjkl", Label: "extend"},
			{Key: "w/b/e", Label: "word", Priority: overlay.HintOptional},
			{Key: "%", Label: "bracket", Priority: overlay.HintOptional},
			{Key: "y", Label: "yank", Priority: overlay.HintEssential},
			{Key: "esc", Label: "cancel"},
		}
	case terminal.CopyModeVisualLine:
		return []overlay.Hint{
			{Key: "jk", Label: "extend"},
			{Key: "y", Label: "yank", Priority: overlay.HintEssential},
			{Key: "esc", Label: "cancel"},
		}
	}
	return nil
}

// modeLegendWidth is the room a legend asks for with every pair shown: the
// strip and a column either side.
func modeLegendWidth(legend []overlay.Hint) int {
	if len(legend) == 0 {
		return 0
	}
	return overlay.HintStripWidth(legend) + 2
}

// renderModeLegend draws a legend as the dock's help block, no wider than
// width: the footer's own strip, on the Panel step the block rests on, with a
// column either side.
func renderModeLegend(legend []overlay.Hint, width int, pal overlay.Palette) string {
	strip, w := overlay.FitHintStrip(legend, width-2, pal.Panel, pal)
	if w == 0 {
		return ""
	}
	pad := overlay.Style(pal.Panel).Render(" ")
	return pad + strip + pad
}
