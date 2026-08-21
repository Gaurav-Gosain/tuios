package app

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// A repainting guest hands us the same bitmap over and over with a little of
// it changed: a browser animating a canvas, a video player, a compositor in a
// pane. Forwarding each one in full is what made an animated page cost tens of
// megabytes a second on the wire, which saturates the host's pty, stalls every
// writer behind it, and starves keystrokes.
//
// Two things fix that, and both need the previous bitmap kept:
//
//   - An identical frame is not sent at all. A page that stops animating stops
//     costing anything.
//   - A frame that differs in part is sent as the parts that differ, patched
//     into the image already on the host with a=f frame edits. The placement
//     is left alone, so there is no delete and no re-place either.
//
// Damage is found on a grid rather than per pixel. A rectangle costs about
// sixty bytes of escape to describe and its pixels are measured in kilobytes,
// so the grid is fine enough that a small moving object does not drag half the
// frame along with it, and coarse enough that the diff itself stays a handful
// of memcmps per band.
const (
	damageBandHeight = 8
	damageTileWidth  = 32
	// maxDamageRects bounds both the escape overhead and the time spent
	// building patches. Past it, one whole-bitmap transmission is cheaper.
	maxDamageRects = 256
	// maxDamageShare is the fraction of the bitmap past which patching stops
	// paying. It is set high because a patch that covers most of the image
	// still costs about what the image costs and still avoids the delete and
	// the re-place; only a frame that has changed almost everywhere is worth
	// sending whole.
	maxDamageShare = 0.9
	// resyncAfterPatches forces a full transmission periodically so a host
	// that quietly dropped the image (kitty enforces a storage quota) is not
	// left patching something that is no longer there.
	resyncAfterPatches = 240
	// maxFullFallbacks is how many frames in a row may fail to patch before
	// the image is written off as undiffable. A video whose every frame
	// changes everywhere should go back to the delivery path built for large
	// frames rather than pay the diff on each one.
	maxFullFallbacks = 8
)

// bitmapCache is the last bitmap sent to the host for one image, kept so the
// next one can be sent as a difference.
type bitmapCache struct {
	data   []byte
	width  int
	height int
	format vt.KittyGraphicsFormat
	// patches counts frames sent as damage since the last whole one, and
	// fullRuns counts whole frames sent in a row where a patch was hoped for.
	patches  int
	fullRuns int
}

// bitmapUpdate says what emitBitmap decided to do with a frame.
type bitmapUpdate int

const (
	// bitmapFull means the caller must transmit the whole bitmap itself.
	bitmapFull bitmapUpdate = iota
	// bitmapUnchanged means the host already shows these exact pixels.
	bitmapUnchanged
	// bitmapPatched means the difference was written as frame edits.
	bitmapPatched
)

// damageRect is one changed rectangle, in pixels.
type damageRect struct{ x, y, w, h int }

// emitBitmap decides how to get a bitmap to the host and, when it can, writes
// the difference from the previous one into pendingOutput. Callers hold kp.mu.
//
// It reports bitmapFull when the caller must send the whole thing, which is
// also the answer whenever anything about the frame is unfamiliar: a new
// image, changed dimensions, a compressed or non-raw payload, or a host that
// does not implement frame edits.
func (kp *KittyPassthrough) emitBitmap(
	windowID string,
	hostID uint32,
	format vt.KittyGraphicsFormat,
	compression vt.KittyGraphicsCompression,
	width, height int,
	raw []byte,
) bitmapUpdate {
	// Only a complete, uncompressed, raw bitmap can be compared or patched.
	// Anything else, including a payload that does not fill the dimensions it
	// declares, says nothing about what the host will end up displaying, and
	// deciding it is unchanged on the strength of equal bytes would suppress a
	// frame that does change the screen.
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

	if !GetHostCapabilities().KittyAnimation || prev.patches >= resyncAfterPatches {
		kp.rememberBitmap(windowID, hostID, format, width, height, raw, false)
		return bitmapFull
	}

	rects := damageRects(prev.data, raw, width, height, bpp)
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

// forwardAnimation passes a guest's a=f / a=a / a=c through to the host,
// under the host's id for the image.
//
// A guest that drives its own animation is the cheapest guest there is: it
// already knows which rectangle changed, so nothing here has to work it out.
// Dropping these commands, which is what used to happen, left such a guest
// with a frozen image and pushed it back to retransmitting whole bitmaps.
//
// When the host cannot animate, the guest is told so rather than left waiting.
// That answer is what makes it fall back, and a fallback that renders beats
// silence that does not.
func (kp *KittyPassthrough) forwardAnimation(cmd *vt.KittyCommand, rawData []byte, windowID string, ptyInput func([]byte)) {
	if !GetHostCapabilities().KittyAnimation {
		if ptyInput != nil && cmd.Quiet < 2 {
			ptyInput(vt.BuildKittyResponse(false, cmd.ImageID,
				"ENOTSUPPORTED:host terminal does not support animation"))
		}
		return
	}

	out := rawData
	if cmd.ImageID != 0 {
		if hostID, ok := kp.imageIDMap[windowID][cmd.ImageID]; ok && hostID != cmd.ImageID {
			out = rewriteKittyImageID(rawData, hostID)
		}
	}

	// The edit changes pixels the host holds, and it is the guest that knows
	// what they now are, so our own record of that bitmap is stale.
	kp.forgetBitmaps(windowID, 0)
	kp.pendingOutput = append(kp.pendingOutput, out...)
}

// rewriteKittyImageID returns an APC sequence with its i= parameter replaced,
// leaving the payload and every other parameter byte-identical. The payload is
// never re-encoded: it may be megabytes, and decoding it to change a number in
// the header would be a waste on every frame.
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

// directFrame buffers one guest transmission that is being forwarded verbatim,
// so the completed bitmap can be compared with the last one before any of it
// goes out.
//
// The original escapes are kept alongside the decoded pixels because the
// fallback has to be byte-identical to what the guest sent: re-encoding a
// transmission we did not fully understand is how a working image turns into
// a broken one.
type directFrame struct {
	imageID     uint32
	format      vt.KittyGraphicsFormat
	compression vt.KittyGraphicsCompression
	width       int
	height      int
	raw         []byte
	data        []byte
}

// absorbDirectFrame buffers a guest's direct transmission and, once it is
// complete, emits either nothing (the host already shows these pixels), the
// difference from the last frame, or the guest's own bytes unchanged.
//
// It reports whether the command was absorbed. A transmission it cannot
// correlate with a previous one, most of all an auto-assigned image id, is
// left to the caller to forward verbatim.
//
// This only runs on the synchronous path, where every byte appended to
// pendingOutput reaches the host in order. A patch is a difference from a
// specific previous frame, so unlike a whole bitmap it can never be dropped;
// the async video paths keep sending whole frames for exactly that reason.
func (kp *KittyPassthrough) absorbDirectFrame(cmd *vt.KittyCommand, rawData []byte, windowID string) bool {
	pending := kp.directFrames[windowID]
	if pending == nil {
		// Only a transmission that names an image and declares raw pixel
		// dimensions can be diffed against its predecessor.
		if cmd.ImageID == 0 || cmd.Width <= 0 || cmd.Height <= 0 ||
			rawBytesPerPixel(cmd.Format) == 0 {
			return false
		}
		if kp.directFrames == nil {
			kp.directFrames = make(map[string]*directFrame)
		}
		pending = &directFrame{
			imageID:     cmd.ImageID,
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
		// A guest that never sends m=0 must not be buffered forever. Flush
		// what there is and go back to forwarding verbatim.
		kp.pendingOutput = append(kp.pendingOutput, pending.raw...)
		delete(kp.directFrames, windowID)
		return true
	}
	if cmd.More {
		return true
	}
	delete(kp.directFrames, windowID)

	switch kp.emitBitmap(windowID, pending.imageID, pending.format,
		pending.compression, pending.width, pending.height, pending.data) {
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

// canPatchBitmap reports whether this image should go down the damage path.
//
// It says yes for an image never seen before, because that first whole frame
// is what a patch is later measured against. It says no once an image has
// repeatedly failed to patch: a stream whose every frame changes everywhere
// gains nothing from the diff and would lose the async queue that keeps its
// large writes off this goroutine.
func (kp *KittyPassthrough) canPatchBitmap(windowID string, hostID uint32) bool {
	if !GetHostCapabilities().KittyAnimation {
		return false
	}
	entry := kp.bitmapCacheFor(windowID, hostID)
	return entry == nil || entry.fullRuns < maxFullFallbacks
}

// hasLiveFrame reports whether this image already has something on the host
// that a dropped frame would leave standing.
//
// Dropping before that is true leaves the pane empty instead of one frame
// stale. It matters most for a remote video stream, where the first reused
// frame is also the one that hands the image over to the self-placing path:
// throw that one away and the stream never starts.
func (kp *KittyPassthrough) hasLiveFrame(windowID string, hostID uint32) bool {
	if kp.remoteVideo[windowID][hostID] != nil {
		return true
	}
	if kp.bitmapCacheFor(windowID, hostID) != nil {
		return true
	}
	p := kp.placements[windowID][hostID]
	return p != nil && !p.Hidden
}

// rawBytesPerPixel returns the pixel stride of a raw kitty format, or 0 for a
// format whose bytes are not pixels and cannot be diffed.
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

// rememberBitmap stores the bitmap the host now holds. The copy is reused
// across frames so a video stream does not allocate a bitmap per frame.
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
		// A new image or a resize starts the count over: the failure to patch
		// was the geometry changing, which says nothing about the content.
		entry.patches = 0
		entry.fullRuns = 0
	}
}

// forgetBitmaps drops the cached bitmaps for a window, or for one image in it
// when hostID is non-zero. Anything that removes the image from the host has
// to call this: patching a bitmap the host no longer holds paints nothing.
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

// damageRects finds the rectangles in which two same-sized bitmaps differ, on
// a coarse grid. It returns nil when the damage is too scattered or too large
// for patching to be worth it, which tells the caller to send the whole
// bitmap instead.
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

// tileDiffers reports whether one tile of the two bitmaps differs.
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

// buildFramePatch writes one damage rectangle as an a=f edit of the image's
// root frame. r=1 names that frame, which is the bitmap the placement already
// shows, and X=1 overwrites rather than blends, so the patch is exactly the
// new pixels. No placement is emitted: the host repaints what is already
// placed.
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

	encoded := base64.StdEncoding.EncodeToString(pixels)
	const chunkSize = 4096
	var out bytes.Buffer
	for i := 0; i < len(encoded); i += chunkSize {
		end := min(i+chunkSize, len(encoded))
		more := end < len(encoded)

		out.WriteString("\x1b_G")
		if i == 0 {
			fmt.Fprintf(&out, "a=f,i=%d,r=1,X=1,f=%d,s=%d,v=%d,x=%d,y=%d,q=2",
				hostID, format, r.w, r.h, r.x, r.y)
		} else {
			fmt.Fprintf(&out, "i=%d,q=2", hostID)
		}
		if more {
			out.WriteString(",m=1")
		} else if i > 0 {
			out.WriteString(",m=0")
		}
		out.WriteByte(';')
		out.WriteString(encoded[i:end])
		out.WriteString("\x1b\\")
	}
	return out.Bytes()
}
