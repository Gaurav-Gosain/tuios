package snapshot

import (
	"errors"
	"fmt"

	uv "github.com/charmbracelet/ultraviolet"

	"tuios.dev/vt"
)

// Apply brings an emulator to the state a snapshot describes. It
// is the reading half of the wire contract Capture writes, and it is
// the whole of what rehydration does to an emulator: the caller owns the
// locking, the window-level flags and the stream that resumes afterwards.
//
// The emulator may be fresh or may be one that survived a workspace switch and
// already holds most of this, so every step is written to be idempotent.
//
// A part of the snapshot that cannot be read is skipped and the rest is
// applied. The error names the parts that were skipped, and is nil when none
// was.
func Apply(t vt.Terminal, state *State) error {
	if t == nil || state == nil {
		return nil
	}
	var dropped []error
	state.Pack()
	styles := resolveStyles(t, state.Styles)

	if state.Width > t.Width() || state.Height > t.Height() {
		t.Resize(max(state.Width, t.Width()), max(state.Height, t.Height()))
	}
	if state.Width > 0 && state.Height > 0 && (state.Width < t.Width() || state.Height < t.Height()) {
		t.Resize(state.Width, state.Height)
	}

	t.RestoreAltScreenMode(state.IsAltScreen)

	if len(state.Modes) > 0 {
		t.RestoreModes(state.Modes)
	}
	if state.ANSIModes != nil {
		t.RestoreANSIModes(state.ANSIModes)
	}
	if len(state.TabStops) > 0 {
		t.RestoreTabStops(tabStopCols(state.TabStops))
	}
	if state.TitlesKnown {
		t.RestoreTitles(titlesFromWire(state))
	}

	if state.KittyKbdUnavailable {
		t.DisableKittyKeyboardProtocol()
	}
	t.RestoreKittyKeyboardState(state.KittyKbdStack)
	t.RestoreKittyKeyboardMainStack(state.KittyKbdMainStack)
	t.RestoreKittyGuestImages(state.KittyImages)
	if state.ModifyOtherKeysKnown || state.ModifyOtherKeys > 0 {
		t.RestoreModifyOtherKeys(state.ModifyOtherKeys)
	}

	if state.Pen != nil {
		t.RestoreCursorPen(styleFromWire(t, *state.Pen))
	}
	if len(state.Margins) == 4 {
		t.RestoreScrollRegion(uv.Rect(state.Margins[0], state.Margins[1], state.Margins[2], state.Margins[3]))
	} else {
		t.ResetScrollRegion()
	}
	if len(state.Charsets) == 6 {
		ids := [4]byte{
			byte(state.Charsets[0]), byte(state.Charsets[1]),
			byte(state.Charsets[2]), byte(state.Charsets[3]),
		}
		t.RestoreCharsets(ids, state.Charsets[4], state.Charsets[5])
	}
	if state.CursorShape > 0 {
		t.RestoreCursorStyle(decscusrStyle(state.CursorShape))
	}
	t.RestoreCursorProtected(state.PenProtected)
	if state.LastPrintedKnown {
		t.RestoreLastPrinted(state.LastPrinted)
	}
	if sc := state.SavedCursor; sc != nil {
		t.RestoreSavedCursor(false, savedCursorFromWire(t, *sc))
	}
	if sc := state.MainSavedCursor; sc != nil && state.IsAltScreen {
		t.RestoreSavedCursor(true, savedCursorFromWire(t, *sc))
	}

	skip := 0
	if have := t.ScrollbackLen(); have > 0 {
		missing := state.ScrollbackLen - have
		skip = packedRowCount(state.PackedScrollback) - max(missing, 0)
	}
	if err := walkPacked(state.PackedScrollback, len(styles), func(y int, row []packedCell) error {
		if y >= skip {
			t.PushScrollbackLine(packedLine(row, styles, make(uv.Line, len(row))))
		}
		return nil
	}); err != nil {
		dropped = append(dropped, fmt.Errorf("snapshot scrollback dropped: %w", err))
	}

	var line uv.Line
	grid := func(blob []byte, set func(x, y int, c *uv.Cell)) bool {
		rows := false
		err := walkPacked(blob, len(styles), func(y int, row []packedCell) error {
			rows = true
			if y >= state.Height {
				return nil
			}
			if cap(line) < len(row) {
				line = make(uv.Line, len(row))
			}
			line = packedLine(row, styles, line[:len(row)])
			for x := 0; x < len(line) && x < state.Width; x++ {
				if line[x].Content == "" {
					continue
				}
				set(x, y, &line[x])
			}
			return nil
		})
		if err != nil {
			dropped = append(dropped, fmt.Errorf("snapshot cells dropped: %w", err))
		}
		return rows
	}
	if grid(state.PackedScreen, t.SetCell) {
		t.RestoreCursorPosition(state.CursorX, state.CursorY)
		t.RestoreCursorPendingWrap(state.PendingWrap)
	}

	grid(state.PackedMain, t.SetMainCell)

	t.RestoreProtectedCells(false, runsFromWire(state.Protected))
	if state.IsAltScreen {
		t.RestoreProtectedCells(true, runsFromWire(state.MainProtected))
	}

	t.RestoreSoftWraps(
		wrapFlags(state.ScreenWraps, state.Height),
		wrapFlags(state.ScrollbackWraps, packedRowCount(state.PackedScrollback)),
	)
	t.RestorePads(
		wrapFlags(state.ScreenPads, state.Height),
		wrapFlags(state.ScrollbackPads, packedRowCount(state.PackedScrollback)),
	)

	if state.GuestColors != nil {
		t.RestoreGuestColors(guestColorsFromWire(t, state.GuestColors))
	}

	pending := state.PendingInput
	if len(pending) > vt.MaxPendingInput {
		dropped = append(dropped, fmt.Errorf("snapshot pending input of %d bytes dropped", len(pending)))
		pending = nil
	}
	t.RestorePendingInput(pending)
	if c := state.OpenCluster; c != nil {
		if oc, ok := t.(openClusters); ok {
			oc.RestoreOpenCluster(vt.OpenCluster{Text: c.Text, X: c.X, Y: c.Y, Width: c.Width, Left: c.Left, Right: c.Right})
		}
	}
	return errors.Join(dropped...)
}

type wireStyle struct {
	style uv.Style
	link  uv.Link
}

func resolveStyles(t vt.Terminal, styles []StyleState) []wireStyle {
	out := make([]wireStyle, len(styles))
	for i, ss := range styles {
		out[i].style, out[i].link = styleFromWire(t, ss)
	}
	return out
}

func packedLine(row []packedCell, styles []wireStyle, line uv.Line) uv.Line {
	for x, pc := range row {
		ws := &styles[pc.style]
		line[x] = uv.Cell{Content: pc.content, Width: pc.width, Style: ws.style, Link: ws.link}
	}
	return line
}
