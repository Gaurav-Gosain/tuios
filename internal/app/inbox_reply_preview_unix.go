//go:build unix

package app

// The kitty half of the reply previews: put each picture on the host after
// the frame that reserved its cells, and take down the ones no frame shows.
// It follows flushLauncherIcons: a picture is uploaded once per image and
// size, placed with the launcher's escapes, and moved only when its cell box
// moved.

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
// no picture, and nothing on the host, it returns at once.
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
	if p == nil || (len(want) == 0 && len(p.shown) == 0) {
		return
	}
	p.mu.Lock()
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
