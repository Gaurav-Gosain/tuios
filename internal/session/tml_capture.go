package session

import (
	"encoding/json"

	"tuios.dev/loom"

	"github.com/Gaurav-Gosain/tuios/internal/jsonx"
	"github.com/Gaurav-Gosain/tuios/internal/theme"
	"github.com/Gaurav-Gosain/tuios/internal/tmldraw"
)

const tmlMaxView = 16 << 10

func tmlDecodeView(raw json.RawMessage) *loom.View {
	if len(raw) == 0 {
		return nil
	}
	var v loom.View
	if jsonx.Unmarshal(raw, &v) != nil {
		return nil
	}
	v.Text, v.Caret = nil, nil
	return &v
}

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

func (d *Daemon) captureTMLWindow(w WindowState, cols, rows int, styled bool) (content string, s *tmlSurface, err error) {
	s = d.tmlSurfaceForWindow(w.ID)
	if s == nil {
		return "", nil, nil
	}
	d.tml.mu.Lock()
	doc := s.doc
	state, _ := jsonx.Marshal(s.store.State())
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
