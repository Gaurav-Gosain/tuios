package app

import (
	"image/color"

	"github.com/Gaurav-Gosain/tuios/internal/composer"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

func composerStyles(pal overlay.Palette) composer.Styles {
	st := composer.Styles{
		Field:     pal.Card,
		Text:      pal.Fg,
		Muted:     pal.FgMute,
		Dim:       pal.FgDim,
		Key:       pal.AccentBright,
		Accent:    pal.Accent,
		Warning:   pal.Warning,
		Selected:  pal.RowSel,
		Selection: pal.Selected,
		ASCII:     overlay.UseASCII(),
		Sigil:     overlay.SigilMark(),
		Readable:  overlay.Readable,
	}
	if pal.Depth == overlay.Depth16 {
		st.Plain, st.Reverse, st.Underline = true, composerReverse, composerUnderline
	} else {
		st.Blend = composerBlend
	}
	return st
}

func composerBlend(a, b color.Color, t float64) color.Color {
	return overlay.Shown(overlay.MixColors(a, b, t))
}

func composerReverse(row string, width int) string {
	return overlay.Palette{Depth: overlay.Depth16}.Row(row, width, overlay.RowState{Cursor: true, Focused: true}, nil)
}

func composerUnderline(span string, _ int) string {
	return overlay.Palette{Depth: overlay.Depth16}.Mark(span, overlay.RowState{Cursor: true})
}

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
