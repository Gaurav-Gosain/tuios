//go:build unix

package app

import (
	"fmt"
	"slices"
)

const replyPreviewHostMax = 8

func (m *OS) replyPixelsReady() bool {
	if m.PostRenderWriter == nil || !m.hostCaps().KittyGraphics {
		return false
	}
	w, h := m.hostCellSize()
	return w > 0 && h > 0
}

func (m *OS) flushReplyPreviewForFrame() {
	p := m.replyPreview
	var want []previewPlacement
	if pl, ok := m.cockpitImagePlacement(); ok && m.replyPixelsReady() && m.overlayOnTop("inbox") && m.ContextMenu == nil {
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
		want = m.quickLookStripPlacements(want)
	}
	if m.replyPixelsReady() {
		want = m.quickLookMDPlacements(want)
	}
	if pl, ok := m.pastePreviewPlacement(); ok && m.replyPixelsReady() {
		want = append(want, pl)
	}
	want = m.threadImagePlacements(want)
	want = m.kbPreviewPlacements(want)
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
	if len(want) == 0 && len(p.shown) == 0 && len(p.gone) == 0 && !p.placeholdersHeld() {
		p.mu.Unlock()
		return
	}
	var buf []byte
	buf = p.flushPlaceholders(buf, want)
	next := make(map[previewID]previewPlacement, len(want))
	for i, pl := range want {
		if pl.virtual {
			continue
		}
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
		buf = appendKittyPlaceRows(buf, at.img, at.placement, pl.x, pl.y, pl.cols, pl.rows, pl.skip, pl.key.rows, pic.w, pic.h)
	}
	for at := range p.shown {
		if _, kept := next[at]; !kept {
			buf = appendKittyUnplace(buf, at.img, at.placement)
		}
	}
	p.trimHost(want)
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

func (p *replyPreview) flushPlaceholders(buf []byte, want []previewPlacement) []byte {
	drawn := map[previewKey]bool{}
	for _, pl := range want {
		if !pl.virtual {
			continue
		}
		drawn[pl.key] = true
		if ph := p.ph[pl.key]; ph != nil {
			buf = p.sendPlaceholder(buf, pl.key, ph)
		}
	}
	for k, ph := range p.ph {
		if drawn[k] || !ph.up {
			continue
		}
		buf = fmt.Appendf(buf, "\x1b_Ga=d,d=I,i=%d,q=2\x1b\\", ph.id)
		ph.up, ph.placed = false, false
	}
	return buf
}

func (p *replyPreview) sendPlaceholder(buf []byte, k previewKey, ph *placeholderPic) []byte {
	pic := p.pics[k]
	if pic == nil || len(pic.png) == 0 {
		return buf
	}
	if !ph.up {
		buf = appendKittyTransmitPNG(buf, ph.id, pic.png)
		ph.up, ph.placed = true, false
	}
	if !ph.placed {
		buf = fmt.Appendf(buf, "\x1b_Ga=p,U=1,i=%d,p=%d,c=%d,r=%d,q=2\x1b\\", ph.id, placeholderVirtual, k.cols, k.rows)
		ph.placed = true
	}
	return buf
}

func (p *replyPreview) placeholdersHeld() bool {
	for _, ph := range p.ph {
		if ph.up {
			return true
		}
	}
	return false
}

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

func (p *replyPreview) hostErased(kept bool) []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !kept {
		clear(p.hostID)
		p.hostOrder = p.hostOrder[:0]
		p.gone = p.gone[:0]
	}
	var buf []byte
	for k, ph := range p.ph {
		if !ph.up {
			continue
		}
		ph.placed = false
		if !kept {
			ph.up = false
		}
		buf = p.sendPlaceholder(buf, k, ph)
	}
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
		var pw, ph int
		if pic := p.pics[pl.key]; pic != nil {
			pw, ph = pic.w, pic.h
		}
		buf = appendKittyPlaceRows(buf, at.img, at.placement, pl.x, pl.y, pl.cols, pl.rows, pl.skip, pl.key.rows, pw, ph)
	}
	return buf
}
