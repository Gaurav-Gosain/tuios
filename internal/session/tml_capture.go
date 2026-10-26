package session

import (
	"encoding/json"

	"tuios.dev/loom"

	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/internal/tmldraw"
)

// capture-pane on a TML window. The window's PTY runs only the placeholder
// line for a client that cannot draw TML, so reading it tells a program
// nothing about what the person sees. The daemon holds the surface's tree
// and state, so it draws the surface itself with loom, as a client does, at
// the size of the window's content box. It needs no attached client, and
// what it draws does not depend on which client asks.
//
// The open tab, the cursor rows and the scroll places are view state, which
// each client keeps. A client reports its view with tml-input when it
// changes, and the draw uses the last report. Typed text that binds no
// state path is never reported, and dropped if a client sends it.

// tmlMaxView bounds the view one tml-input reports.
const tmlMaxView = 16 << 10

// tmlDecodeView decodes a reported view, or returns nil.
func tmlDecodeView(raw json.RawMessage) *loom.View {
	if len(raw) == 0 {
		return nil
	}
	var v loom.View
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	v.Text, v.Caret = nil, nil
	return &v
}

// tmlSurfaceForWindow returns the open surface drawn in a window, or nil.
func (d *Daemon) tmlSurfaceForWindow(windowID string) *tmlSurface {
	d.tml.mu.Lock()
	defer d.tml.mu.Unlock()
	for _, s := range d.tml.surfaces {
		if s.windowID == windowID && !s.closed {
			return s
		}
	}
	return nil
}

// captureTMLWindow draws the surface in window w, cols by rows cells, as a
// client draws it in that content box. ok is false when no surface is open
// in the window.
func (d *Daemon) captureTMLWindow(w WindowState, cols, rows int, styled bool) (content string, s *tmlSurface, err error) {
	s = d.tmlSurfaceForWindow(w.ID)
	if s == nil {
		return "", nil, nil
	}
	d.tml.mu.Lock()
	doc := s.doc
	state, _ := json.Marshal(s.store.State())
	view := tmlDecodeView(s.view)
	d.tml.mu.Unlock()
	if doc == nil {
		return "", nil, nil
	}
	placement := "pane"
	if w.Popup {
		placement = "popup"
	}
	content, err = tmldraw.Draw(doc, tmldraw.Options{
		Width: cols, Height: rows, Inset: true,
		Placement: placement,
		State:     state,
		View:      view,
		Styled:    styled,
		Palette:   tmldraw.Palette(theme.UI(), false),
	})
	return content, s, err
}

// captureTMLPane is capture-pane on a TML window: the surface drawn at the
// window's size. It returns nil and no error when no surface is open in the
// window, and capture-pane then reads the PTY as for any window.
func (d *Daemon) captureTMLPane(w WindowState, pty *PTY, styled, resolved bool, palette []string, trim bool, start, end, lines int) (map[string]any, *verbError) {
	cols, rows := pty.Size()
	content, s, err := d.captureTMLWindow(w, cols, rows, styled)
	if err != nil {
		return nil, newVerbError(ErrVerbInternal, "could not draw the TML surface: "+err.Error())
	}
	if s == nil {
		return nil, nil
	}
	if resolved {
		pal, verr := paletteFromParams(palette)
		if verr != nil {
			return nil, verr
		}
		content = ResolveSGR(content, pal)
	}
	if trim {
		content = trimCapture(content)
	}
	omitted := -1
	if lines > 0 && start <= 0 && end <= 0 {
		omitted = max(captureLineCount(content)-lines, 0)
	}
	content = sliceCaptureLines(content, start, end, lines)
	d.tml.mu.Lock()
	id, version := s.id, s.version
	d.tml.mu.Unlock()
	out := map[string]any{
		"type":            "pane_content",
		"content":         content,
		"source":          "visible",
		"styled":          styled,
		"resolved":        resolved,
		"history_rows":    0,
		"revision":        version,
		"surface":         id,
		"surface_version": version,
	}
	if trim {
		out["trimmed"] = true
	}
	if omitted >= 0 {
		out["lines_omitted"] = omitted
	}
	if d.events != nil {
		out["boot_id"] = d.events.bootIdentity()
	}
	return out, nil
}
