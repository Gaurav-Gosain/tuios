//go:build unix

package app

import (
	"fmt"
	"slices"
)

// The kitty half of the reply previews: put each picture on the host after
// the frame that reserved its cells, and take down the ones no frame shows.
// It follows flushLauncherIcons: a picture is uploaded once per image and
// size, placed with the launcher's escapes, and moved only when its cell box
// moved.
//
// What the host holds is written down here, and two things change it without a
// word back. An ED 2 drops every picture, which keepPicturesFrame takes out of
// the frame and hostErased hears about. A host
// over its storage quota drops the oldest, kitty at 320 MB, which
// replyPreviewHostMax keeps these from reaching. A placement of a picture the
// host dropped fails, and with q=2 it fails in silence.

// replyPreviewHostMax bounds the pictures left on the host. Eight full-size
// pictures are under 50 MB there, and moving back over the last few files costs
// a placement instead of an upload.
const replyPreviewHostMax = 8

// replyPixelsReady reports whether the previews can be kitty pictures.
func (m *OS) replyPixelsReady() bool {
	if m.PostRenderWriter == nil || !m.hostCaps().KittyGraphics {
		return false
	}
	w, h := m.hostCellSize()
	return w > 0 && h > 0
}

// flushReplyPreviewForFrame draws the open draft's pictures for the frame
// just rendered, in the Inbox or the mailbox. With no draft open, or one with
// no picture, nothing on the host and no picture waiting to be freed, it
// returns at once.
func (m *OS) flushReplyPreviewForFrame() {
	p := m.replyPreview
	var want []previewPlacement
	if pl, ok := m.cockpitImagePlacement(); ok && m.replyPixelsReady() && m.overlayOnTop("inbox") && m.ContextMenu == nil {
		// The image view covers the draft: its picture is the one shown.
		want = append(want, pl)
	} else if d, ok := m.openDraft(); ok && d.wires.at.ok && m.replyPixelsReady() {
		if h, ok := m.overlayHitByKind(d.kind); ok && m.overlayOnTop(d.kind) {
			at := d.wires.at
			for _, pl := range d.comp.Placements() {
				want = append(want, previewPlacement{
					key:  pixelsKey(pl.Image, pl.Cols, pl.Rows),
					x:    h.OriginX + at.x + pl.Col,
					y:    h.OriginY + at.y + pl.Row,
					cols: pl.Cols, rows: pl.Rows,
				})
			}
		}
	}
	if pl, ok := m.quickLookPlacement(); ok && m.replyPixelsReady() {
		want = append(want, pl)
	}
	if m.previewResizing() {
		want = nil
	}
	if p == nil {
		return
	}
	if !p.eraseHooked && m.PostRenderWriter != nil && len(want) > 0 {
		p.eraseHooked = true
		m.PostRenderWriter.AddEraseHook(p.hostErased)
	}
	p.mu.Lock()
	if len(want) == 0 && len(p.shown) == 0 && len(p.gone) == 0 {
		p.mu.Unlock()
		return
	}
	var buf []byte
	next := make(map[previewID]previewPlacement, len(want))
	for i, pl := range want {
		pic := p.pics[pl.key]
		if pic == nil || len(pic.png) == 0 {
			continue
		}
		id, up := p.hostID[pl.key]
		if !up {
			id = p.nextID
			p.nextID++
			p.hostID[pl.key] = id
			p.hostOrder = append(p.hostOrder, pl.key)
			buf = appendKittyTransmitPNG(buf, id, pic.png)
		}
		at := previewID{img: id, placement: uint32(i + 1)}
		next[at] = pl
		if was, drawn := p.shown[at]; drawn && was == pl {
			continue
		} else if drawn {
			buf = appendKittyUnplace(buf, at.img, at.placement)
		}
		buf = appendKittyPlaceBox(buf, at.img, at.placement, pl.x, pl.y, pl.cols, pl.rows)
	}
	for at := range p.shown {
		if _, kept := next[at]; !kept {
			buf = appendKittyUnplace(buf, at.img, at.placement)
		}
	}
	p.trimHost(want)
	// An uppercase delete frees the image data; the unplace above keeps it.
	for _, id := range p.gone {
		buf = fmt.Appendf(buf, "\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", id)
	}
	p.gone = p.gone[:0]
	p.shown = next
	p.mu.Unlock()
	if len(buf) == 0 {
		return
	}
	out := wrapSync(buf)
	if m.PostRenderWriter != nil {
		m.PostRenderWriter.QueuePostRender(out)
		return
	}
	m.WriteHost(out)
}

// trimHost sends the oldest uploads past replyPreviewHostMax to the gone list,
// other than the ones on screen. Call it with p.mu held.
func (p *replyPreview) trimHost(want []previewPlacement) {
	over := len(p.hostOrder) - replyPreviewHostMax
	if over <= 0 {
		return
	}
	kept := p.hostOrder[:0]
	for _, k := range p.hostOrder {
		onScreen := slices.ContainsFunc(want, func(pl previewPlacement) bool { return pl.key == k })
		if over > 0 && !onScreen {
			p.gone = append(p.gone, p.hostID[k])
			delete(p.hostID, k)
			over--
			continue
		}
		kept = append(kept, k)
	}
	p.hostOrder = kept
}

// hostErased runs in the writer for a frame that erases the host's screen. What
// the last frame placed goes out again behind the frame, so no later frame need
// come to carry it. When kept is false the host dropped the pictures too, so
// nothing counts as uploaded and those pictures are sent again. See
// PostRenderWriter.AddEraseHook.
func (p *replyPreview) hostErased(kept bool) []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !kept {
		clear(p.hostID)
		p.hostOrder = p.hostOrder[:0]
		p.gone = p.gone[:0]
	}
	var buf []byte
	for at, pl := range p.shown {
		if _, up := p.hostID[pl.key]; !up {
			pic := p.pics[pl.key]
			if pic == nil || len(pic.png) == 0 {
				delete(p.shown, at)
				continue
			}
			p.hostID[pl.key] = at.img
			p.hostOrder = append(p.hostOrder, pl.key)
			buf = appendKittyTransmitPNG(buf, at.img, pic.png)
		}
		buf = appendKittyPlaceBox(buf, at.img, at.placement, pl.x, pl.y, pl.cols, pl.rows)
	}
	return buf
}
