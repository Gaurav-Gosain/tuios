package app

import (
	"image/color"

	"github.com/Gaurav-Gosain/tuios/internal/composer"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// composerStyles is the composer's look in pal. The composer takes no colour
// of its own, so every editor in tuios is drawn in the chrome palette.
func composerStyles(pal overlay.Palette) composer.Styles {
	st := composer.Styles{
		Field:    pal.Card,
		Text:     pal.Fg,
		Muted:    pal.FgMute,
		Dim:      pal.FgDim,
		Key:      pal.AccentBright,
		Accent:   pal.Accent,
		Warning:  pal.Warning,
		Selected: pal.RowSel,
		ASCII:    overlay.UseASCII(),
		Sigil:    overlay.SigilMark(),
		Readable: overlay.Readable,
	}
	if pal.Depth == overlay.Depth16 {
		st.Plain, st.Reverse, st.Underline = true, composerReverse, composerUnderline
	} else {
		st.Blend = composerBlend
	}
	return st
}

// composerBlend is a block's ground: a mix in OKLab, as the depth shows it.
func composerBlend(a, b color.Color, t float64) color.Color {
	return overlay.Shown(overlay.MixColors(a, b, t))
}

// composerReverse marks a selected row at 16 colours, as a list does.
func composerReverse(row string, width int) string {
	return overlay.Palette{Depth: overlay.Depth16}.Row(row, width, overlay.RowState{Cursor: true, Focused: true}, nil)
}

// composerUnderline marks a selected block at 16 colours.
func composerUnderline(span string, _ int) string {
	return overlay.Palette{Depth: overlay.Depth16}.Mark(span, overlay.RowState{Cursor: true})
}

// composerHints are the composer's keys as footer hints.
func composerHints(hints []composer.Hint) []overlay.Hint {
	out := make([]overlay.Hint, len(hints))
	for i, h := range hints {
		out[i] = overlay.Hint{Key: h.Key, Label: h.Label}
		if h.Optional {
			out[i].Priority = overlay.HintOptional
		}
	}
	return out
}
