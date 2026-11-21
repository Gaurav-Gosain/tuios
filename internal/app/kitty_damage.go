package app

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"strings"

	"tuios.dev/vt"
)

const (
	damageBandHeight   = 8
	damageTileWidth    = 32
	maxDamageRects     = 256
	maxDamageShare     = 0.9
	resyncAfterPatches = 240
	maxFullFallbacks   = 8
	maxPatchPayload    = 4096 / 4 * 3
)

type bitmapCache struct {
	data     []byte
	width    int
	height   int
	format   vt.KittyGraphicsFormat
	patches  int
	fullRuns int
}

type bitmapUpdate int

const (
	bitmapFull bitmapUpdate = iota
	bitmapUnchanged
	bitmapPatched
)

type damageRect struct{ x, y, w, h int }

func (kp *KittyPassthrough) emitBitmap(
	windowID string,
	hostID uint32,
	format vt.KittyGraphicsFormat,
	compression vt.KittyGraphicsCompression,
	width, height int,
	raw []byte,
	patchable bool,
) bitmapUpdate {
	if !patchable {
		kp.forgetBitmaps(windowID, hostID)
		return bitmapFull
	}

	bpp := rawBytesPerPixel(format)
	if bpp == 0 || width <= 0 || height <= 0 ||
		compression != vt.KittyCompressionNone ||
		len(raw) != width*height*bpp {
		kp.forgetBitmaps(windowID, hostID)
		return bitmapFull
	}

	prev := kp.bitmapCacheFor(windowID, hostID)
	if prev == nil || prev.width != width || prev.height != height ||
		prev.format != format || len(prev.data) != len(raw) {
		kp.rememberBitmap(windowID, hostID, format, width, height, raw, false)
		return bitmapFull
	}

	if bytes.Equal(prev.data, raw) {
		return bitmapUnchanged
	}

	if !kp.hostCaps().KittyAnimation || prev.patches >= resyncAfterPatches {
		kp.rememberBitmap(windowID, hostID, format, width, height, raw, false)
		return bitmapFull
	}

	rects := splitPatchRects(damageRects(prev.data, raw, width, height, bpp), bpp)
	if len(rects) == 0 {
		kp.rememberBitmap(windowID, hostID, format, width, height, raw, false)
		return bitmapFull
	}

	for _, r := range rects {
		kp.pendingOutput = append(kp.pendingOutput,
			buildFramePatch(hostID, format, raw, width, bpp, r)...)
	}
	kp.rememberBitmap(windowID, hostID, format, width, height, raw, true)
	return bitmapPatched
}

func (kp *KittyPassthrough) forwardAnimation(cmd *vt.KittyCommand, rawData []byte, windowID string, ptyInput func([]byte)) {
	if !kp.hostCaps().KittyAnimation {
		if ptyInput != nil && cmd.Quiet < 2 {
			ptyInput(vt.BuildKittyResponse(false, cmd.ImageID, vt.KittyAnimationRefusal))
		}
		return
	}

	out := rawData
	var hostID uint32
	if cmd.ImageID != 0 {
		hostID = kp.getOrAllocateHostID(windowID, cmd.ImageID)
		out = rewriteKittyImageID(rawData, hostID)
	}

	kp.forgetBitmaps(windowID, 0)
	kp.pendingOutput = append(kp.pendingOutput, out...)

	if cmd.Action == vt.KittyActionFrame && kp.editShowsAtOnce(windowID, hostID) {
		kp.flushToHost()
	}
}

func (kp *KittyPassthrough) editShowsAtOnce(windowID string, hostID uint32) bool {
	if hostID == 0 || kp.hostOut == nil || kp.held[windowID] != nil {
		return false
	}
	if probe := kp.syncProbes[windowID]; probe != nil {
		if open, _ := probe(); open {
			return false
		}
	}
	p := kp.placements[windowID][imageKey(hostID)]
	return p != nil && !p.Hidden && !p.DataDirty && p.PlacementID != 0
}

func rewriteKittyImageID(rawData []byte, hostID uint32) []byte {
	const prefix = "\x1b_G"
	if !bytes.HasPrefix(rawData, []byte(prefix)) {
		return rawData
	}
	body := rawData[len(prefix):]
	semi := bytes.IndexByte(body, ';')
	if semi < 0 {
		semi = len(body)
		if end := bytes.Index(body, []byte("\x1b\\")); end >= 0 {
			semi = end
		}
	}
	params := string(body[:semi])

	var out bytes.Buffer
	out.WriteString(prefix)
	for i, pair := range strings.Split(params, ",") {
		if i > 0 {
			out.WriteByte(',')
		}
		if strings.HasPrefix(pair, "i=") {
			fmt.Fprintf(&out, "i=%d", hostID)
			continue
		}
		out.WriteString(pair)
	}
	out.Write(body[semi:])
	return out.Bytes()
}

type directFrame struct {
	imageID     uint32
	guestID     uint32
	format      vt.KittyGraphicsFormat
	compression vt.KittyGraphicsCompression
	width       int
	height      int
	raw         []byte
	data        []byte
}

func (kp *KittyPassthrough) absorbDirectFrame(cmd *vt.KittyCommand, rawData []byte, windowID string, hostID uint32) bool {
	pending := kp.directFrames[windowID]
	if pending == nil {
		if cmd.ImageID == 0 || cmd.Width <= 0 || cmd.Height <= 0 ||
			rawBytesPerPixel(cmd.Format) == 0 {
			return false
		}
		if kp.directFrames == nil {
			kp.directFrames = make(map[string]*directFrame)
		}
		pending = &directFrame{
			imageID:     hostID,
			guestID:     cmd.ImageID,
			format:      cmd.Format,
			compression: cmd.Compression,
			width:       cmd.Width,
			height:      cmd.Height,
		}
		kp.directFrames[windowID] = pending
	}

	pending.raw = append(pending.raw, rawData...)
	pending.data = append(pending.data, cmd.Data...)
	if len(pending.raw) > maxPassthroughTransmitBytes {
		kp.pendingOutput = append(kp.pendingOutput, pending.raw...)
		delete(kp.directFrames, windowID)
		return true
	}
	if cmd.More {
		return true
	}
	delete(kp.directFrames, windowID)
	kp.keepImage(windowID, pending.imageID, &keptImage{
		guestID: pending.guestID, format: pending.format, compression: pending.compression,
		width: pending.width, height: pending.height, data: pending.data,
	})

	switch kp.emitBitmap(windowID, pending.imageID, pending.format,
		pending.compression, pending.width, pending.height, pending.data, true) {
	case bitmapUnchanged:
		kittyPassthroughLog("absorbDirectFrame: identical frame for i=%d, sending nothing", pending.imageID)
	case bitmapPatched:
		kittyPassthroughLog("absorbDirectFrame: patched i=%d (%d raw bytes saved)",
			pending.imageID, len(pending.raw))
	default:
		kp.pendingOutput = append(kp.pendingOutput, pending.raw...)
	}
	return true
}

func (kp *KittyPassthrough) canPatchBitmap(windowID string, hostID uint32) bool {
	if !kp.hostCaps().KittyAnimation {
		return false
	}
	entry := kp.bitmapCacheFor(windowID, hostID)
	return entry == nil || entry.fullRuns < maxFullFallbacks
}

func (kp *KittyPassthrough) hasLiveFrame(windowID string, hostID uint32) bool {
	if !kp.remoteClient {
		return true
	}
	return kp.remoteVideo[windowID][hostID] != nil
}

func rawBytesPerPixel(f vt.KittyGraphicsFormat) int {
	switch f {
	case vt.KittyFormatRGB:
		return 3
	case vt.KittyFormatRGBA:
		return 4
	default:
		return 0
	}
}

func (kp *KittyPassthrough) bitmapCacheFor(windowID string, hostID uint32) *bitmapCache {
	if kp.lastBitmap == nil {
		return nil
	}
	return kp.lastBitmap[windowID][hostID]
}

func (kp *KittyPassthrough) rememberBitmap(
	windowID string,
	hostID uint32,
	format vt.KittyGraphicsFormat,
	width, height int,
	raw []byte,
	patched bool,
) {
	if kp.lastBitmap == nil {
		kp.lastBitmap = make(map[string]map[uint32]*bitmapCache)
	}
	if kp.lastBitmap[windowID] == nil {
		kp.lastBitmap[windowID] = make(map[uint32]*bitmapCache)
	}
	entry := kp.lastBitmap[windowID][hostID]
	if entry == nil {
		entry = &bitmapCache{}
		kp.lastBitmap[windowID][hostID] = entry
	}
	sameGeometry := entry.width == width && entry.height == height && entry.format == format
	entry.data = append(entry.data[:0], raw...)
	entry.width, entry.height, entry.format = width, height, format
	switch {
	case patched:
		entry.patches++
		entry.fullRuns = 0
	case sameGeometry:
		entry.patches = 0
		entry.fullRuns++
	default:
		entry.patches = 0
		entry.fullRuns = 0
	}
}

func (kp *KittyPassthrough) forgetBitmaps(windowID string, hostID uint32) {
	if kp.lastBitmap == nil {
		return
	}
	if hostID == 0 {
		delete(kp.lastBitmap, windowID)
		return
	}
	if m := kp.lastBitmap[windowID]; m != nil {
		delete(m, hostID)
	}
}

func damageRects(prev, cur []byte, width, height, bpp int) []damageRect {
	stride := width * bpp
	var rects []damageRect
	damaged := 0
	limit := int(float64(width*height*bpp) * maxDamageShare)

	for top := 0; top < height; top += damageBandHeight {
		bandH := min(damageBandHeight, height-top)
		runStart := -1
		for tile := 0; tile*damageTileWidth < width; tile++ {
			x := tile * damageTileWidth
			w := min(damageTileWidth, width-x)
			changed := tileDiffers(prev, cur, stride, bpp, x, top, w, bandH)
			switch {
			case changed && runStart < 0:
				runStart = x
			case !changed && runStart >= 0:
				rects = append(rects, damageRect{runStart, top, x - runStart, bandH})
				damaged += (x - runStart) * bandH * bpp
				runStart = -1
			}
			if len(rects) > maxDamageRects || damaged > limit {
				return nil
			}
		}
		if runStart >= 0 {
			rects = append(rects, damageRect{runStart, top, width - runStart, bandH})
			damaged += (width - runStart) * bandH * bpp
			if len(rects) > maxDamageRects || damaged > limit {
				return nil
			}
		}
	}
	return rects
}

func tileDiffers(prev, cur []byte, stride, bpp, x, y, w, h int) bool {
	off := x * bpp
	n := w * bpp
	for row := y; row < y+h; row++ {
		lo := row*stride + off
		if !bytes.Equal(prev[lo:lo+n], cur[lo:lo+n]) {
			return true
		}
	}
	return false
}

func splitPatchRects(rects []damageRect, bpp int) []damageRect {
	if bpp <= 0 {
		return nil
	}
	maxW := maxPatchPayload / bpp
	if maxW < 1 {
		return nil
	}
	out := rects[:0:0]
	for _, r := range rects {
		for x := 0; x < r.w; x += maxW {
			w := min(maxW, r.w-x)
			rows := max(maxPatchPayload/(w*bpp), 1)
			for y := 0; y < r.h; y += rows {
				out = append(out, damageRect{
					x: r.x + x, y: r.y + y, w: w, h: min(rows, r.h-y),
				})
			}
		}
	}
	return out
}

func buildFramePatch(
	hostID uint32,
	format vt.KittyGraphicsFormat,
	raw []byte,
	width, bpp int,
	r damageRect,
) []byte {
	stride := width * bpp
	rowBytes := r.w * bpp
	pixels := make([]byte, 0, rowBytes*r.h)
	for row := r.y; row < r.y+r.h; row++ {
		lo := row*stride + r.x*bpp
		pixels = append(pixels, raw[lo:lo+rowBytes]...)
	}

	if len(pixels) > maxPatchPayload {
		return nil
	}
	var out bytes.Buffer
	fmt.Fprintf(&out, "\x1b_Ga=f,i=%d,r=1,X=1,f=%d,s=%d,v=%d,x=%d,y=%d,q=2;",
		hostID, format, r.w, r.h, r.x, r.y)
	out.WriteString(base64.StdEncoding.EncodeToString(pixels))
	out.WriteString("\x1b\\")
	return out.Bytes()
}
