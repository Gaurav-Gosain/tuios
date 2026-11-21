package app

import (
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
	"github.com/Gaurav-Gosain/tuios/internal/terminal"
)

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

func paneLabelsLegend() []overlay.Hint {
	return []overlay.Hint{
		{Key: "label", Label: "focus pane", Priority: overlay.HintEssential},
		{Key: "backspace", Label: "undo key", Priority: overlay.HintOptional},
		{Key: "esc", Label: "cancel", Priority: overlay.HintEssential},
	}
}

func (m *OS) copyModeExitKey() string {
	if m.KeybindRegistry == nil {
		return ""
	}
	keys := m.KeybindRegistry.Keys(config.TableCopyMode, config.ActionCopyModeExit)
	if len(keys) == 0 {
		return ""
	}
	if keys[0] == "enter" {
		return overlay.EnterKey()
	}
	return keys[0]
}

func copyModeLegend(state terminal.CopyModeState, exit string) []overlay.Hint {
	quit := "q/esc"
	if exit != "" {
		quit = "q/" + exit + "/esc"
	}
	exitYank := overlay.Hint{Key: exit, Label: "yank and quit", Priority: overlay.HintOptional}
	visual := func(h []overlay.Hint) []overlay.Hint {
		if exit == "" {
			return h
		}
		for i := range h {
			if h[i].Key == "esc" {
				return append(h[:i:i], append([]overlay.Hint{exitYank}, h[i:]...)...)
			}
		}
		return append(h, exitYank)
	}
	switch state {
	case terminal.CopyModeNormal:
		return []overlay.Hint{
			{Key: "hjkl", Label: "move"},
			{Key: "w/b/e", Label: "word", Priority: overlay.HintOptional},
			{Key: "f/t", Label: "char", Priority: overlay.HintOptional},
			{Key: "/", Label: "search"},
			{Key: "n/N", Label: "next", Priority: overlay.HintOptional},
			{Key: "v", Label: "visual"},
			{Key: "ctrl+v", Label: "block", Priority: overlay.HintOptional},
			{Key: "y", Label: "yank", Priority: overlay.HintEssential},
			{Key: quit, Label: "quit", Priority: overlay.HintEssential},
			{Key: "ctrl+c", Label: "quit", Priority: overlay.HintOptional},
		}
	case terminal.CopyModeSearch:
		return []overlay.Hint{
			{Key: "type", Label: "search", Priority: overlay.HintOptional},
			{Key: "n/N", Label: "next"},
			{Key: overlay.EnterKey(), Label: "done", Priority: overlay.HintEssential},
			{Key: "esc", Label: "cancel"},
			{Key: "ctrl+c", Label: "quit", Priority: overlay.HintOptional},
		}
	case terminal.CopyModeVisualChar:
		return visual([]overlay.Hint{
			{Key: "hjkl", Label: "extend"},
			{Key: "w/b/e", Label: "word", Priority: overlay.HintOptional},
			{Key: "%", Label: "bracket", Priority: overlay.HintOptional},
			{Key: "y", Label: "yank", Priority: overlay.HintEssential},
			{Key: "esc", Label: "cancel"},
			{Key: "ctrl+c", Label: "quit", Priority: overlay.HintOptional},
		})
	case terminal.CopyModeVisualBlock:
		return visual([]overlay.Hint{
			{Key: "hjkl", Label: "extend"},
			{Key: "o", Label: "corner", Priority: overlay.HintOptional},
			{Key: "y", Label: "yank", Priority: overlay.HintEssential},
			{Key: "esc", Label: "cancel"},
			{Key: "ctrl+c", Label: "quit", Priority: overlay.HintOptional},
		})
	case terminal.CopyModeVisualLine:
		return visual([]overlay.Hint{
			{Key: "jk", Label: "extend"},
			{Key: "y", Label: "yank", Priority: overlay.HintEssential},
			{Key: "esc", Label: "cancel"},
			{Key: "ctrl+c", Label: "quit", Priority: overlay.HintOptional},
		})
	}
	return nil
}

func modeLegendWidth(legend []overlay.Hint) int {
	if len(legend) == 0 {
		return 0
	}
	return overlay.HintStripWidth(legend) + 2
}

func renderModeLegend(legend []overlay.Hint, width int, pal overlay.Palette) string {
	strip, w := overlay.FitHintStrip(legend, width-2, pal.Panel, pal)
	if w == 0 {
		return ""
	}
	pad := overlay.Style(pal.Panel).Render(" ")
	return pad + strip + pad
}
