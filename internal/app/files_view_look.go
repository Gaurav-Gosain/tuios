package app

import (
	"slices"

	tea "charm.land/bubbletea/v2"
	"github.com/Gaurav-Gosain/tuios/internal/config"
	"github.com/Gaurav-Gosain/tuios/internal/overlay"
)

// filesLookHeader is the rows quick look draws above its body beside the
// Files view's list, in place of its frame.
const filesLookHeader = 1

// quickLookFilesSync is QuickLookSync while the Files view owns the preview:
// it follows the view's cursor, and goes when the view or its look goes.
func (m *OS) quickLookFilesSync() tea.Cmd {
	ql := &m.quickLook
	if !m.explorer.open || !m.explorer.look {
		m.dropQuickLook()
		return nil
	}
	t, ok := m.filesLookTarget()
	if ql.walked {
		// The view's cursor moved to another row: the preview follows it
		// again.
		if !ok || t == ql.walkFrom {
			return nil
		}
		ql.walked, ql.rejoin, ql.inside, ql.dirWant = false, false, false, ""
	}
	if !ok || t == ql.target {
		return nil
	}
	return m.quickLookShow(t)
}

// quickLookCovered reports whether a panel is over the preview, which then
// draws no picture. The Files view is under its own preview.
func (m *OS) quickLookCovered() bool {
	if !m.AnyOverlayOpen() {
		return false
	}
	return !m.quickLook.files || m.topmostOverlayKind() != overlayKindFiles
}

// filesLookHeader draws quick look's header beside the Files view's list:
// the file's mark and name, and on the right the keys that go into it. It
// sits on the row of the list's machine row, on the same ground.
func (m *OS) filesLookHeader(pal overlay.Palette, w int) string {
	ql := &m.quickLook
	c := (*ckCanvas)(nil).reset(w, 1, ckColor(pal.Panel))
	t := ql.target
	icon := fileIconFor(t.name, t.dir)
	name := t.name
	if t.dir {
		name += "/"
	}
	x := 1
	if icon.Glyph != "" && !m.Settings.NoNerdFont && !overlay.UseASCII() {
		x = c.text(x, 0, w-1, ckSeg{t: icon.Glyph + " ", fg: pal.AccentBright})
	}
	c.text(x, 0, w-1, ckSeg{t: name, fg: pal.Fg, attrs: ckBold})
	seg := func(segs []ckSeg, key, label string) []ckSeg {
		if len(segs) > 0 {
			segs = append(segs, ckSeg{t: "   "})
		}
		return append(segs, ckSeg{t: key, fg: pal.AccentBright, attrs: ckBold}, ckSeg{t: " " + label, fg: pal.FgDim})
	}
	hint := func(segs []ckSeg, table config.Table, action, label string) []ckSeg {
		if m.KeybindRegistry == nil {
			return segs
		}
		keys := m.KeybindRegistry.Keys(table, action)
		if len(keys) == 0 {
			return segs
		}
		return seg(segs, keys[0], label)
	}
	var lead, full []ckSeg
	switch q := ql.copy; {
	case q != nil && !q.cm.Implicit && q.cm.State.InVisual():
		// Copy mode has its own keys, the ones its legend names.
		lead = seg(seg(nil, "y", "copy"), "esc", "end the selection")
	case q != nil && !q.cm.Implicit:
		lead = seg(seg(nil, "v", "select"), "q", "leave copy mode")
	case ql.inside:
		lead = hint(lead, config.TableQuickLookInside, "go_back", "back to the list")
	default:
		lead = hint(lead, config.TableQuickLook, "go_inside", "read inside")
	}
	full = lead
	if ql.cursorShown() {
		full = hint(slices.Clone(lead), config.TableQuickLookInside, "select_text", "select")
		full = hint(full, config.TableQuickLookInside, config.ActionCopyMode, "copy mode")
	}
	lead = hint(lead, config.TableFiles, config.ActionFilesFill, "fill")
	full = hint(full, config.TableFiles, config.ActionFilesFill, "fill")
	// A narrow header drops the copy keys first, then every key.
	for _, segs := range [...][]ckSeg{full, lead} {
		if ckWidth(segs...)+ckWidth(ckSeg{t: name})+6 < w {
			c.textR(w-1, 0, segs...)
			break
		}
	}
	return c.render()
}
