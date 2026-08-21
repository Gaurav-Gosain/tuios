package vt

import (
	"time"
)

// Animation reuses the placement keys for entirely different meanings, so the
// parser stores them once and these accessors name what each one is under
// a=f, a=a and a=c. Reading cmd.Rows where the spec says "frame number" is
// how the two sets of meanings get confused.
func (c *KittyCommand) frameNumber() int   { return c.Rows }         // r
func (c *KittyCommand) baseFrame() int     { return c.Columns }      // c
func (c *KittyCommand) frameGap() int      { return int(c.ZIndex) }  // z
func (c *KittyCommand) animState() int     { return c.Width }        // s
func (c *KittyCommand) animLoops() int     { return c.Height }       // v
func (c *KittyCommand) blendMode() int     { return c.XOffset }      // X
func (c *KittyCommand) composeMode() int   { return c.CursorMove }   // C
func (c *KittyCommand) destX() int         { return c.SourceX }      // x
func (c *KittyCommand) destY() int         { return c.SourceY }      // y
func (c *KittyCommand) composeSrcX() int   { return c.XOffset }      // X
func (c *KittyCommand) composeSrcY() int   { return c.YOffset }      // Y
func (c *KittyCommand) composeWidth() int  { return c.SourceWidth }  // w
func (c *KittyCommand) composeHeight() int { return c.SourceHeight } // h

// bytesPerPixel returns the stride of one pixel in a raw pixel format, or 0
// for a format that is not raw (PNG), which cannot be composed without being
// decoded first.
func bytesPerPixel(f KittyGraphicsFormat) int {
	switch f {
	case KittyFormatRGB:
		return 3
	case KittyFormatRGBA:
		return 4
	default:
		return 0
	}
}

// FrameCount returns the number of frames, counting the root frame.
func (img *KittyImage) FrameCount() int { return 1 + len(img.Frames) }

// frameData returns the pixels of the 1-based frame n, or nil if there is no
// such frame.
func (img *KittyImage) frameData(n int) []byte {
	switch {
	case n == 1:
		return img.Data
	case n >= 2 && n-2 < len(img.Frames):
		return img.Frames[n-2].Data
	default:
		return nil
	}
}

// setFrameData replaces the pixels of the 1-based frame n.
func (img *KittyImage) setFrameData(n int, data []byte) {
	switch {
	case n == 1:
		img.Data = data
	case n >= 2 && n-2 < len(img.Frames):
		img.Frames[n-2].Data = data
	}
}

// frameGap returns the hold time of the 1-based frame n in milliseconds.
func (img *KittyImage) frameGap(n int) int {
	gap := 0
	switch {
	case n == 1:
		gap = img.RootGap
	case n >= 2 && n-2 < len(img.Frames):
		gap = img.Frames[n-2].Gap
	}
	if gap == 0 {
		return defaultKittyGap
	}
	return gap
}

// setFrameGap sets the hold time of the 1-based frame n.
func (img *KittyImage) setFrameGap(n, gap int) {
	switch {
	case n == 1:
		img.RootGap = gap
	case n >= 2 && n-2 < len(img.Frames):
		img.Frames[n-2].Gap = gap
	}
}

// pixelBytes is how many bytes one full frame of this image occupies.
func (img *KittyImage) pixelBytes() int {
	bpp := bytesPerPixel(img.Format)
	if bpp == 0 || img.Width <= 0 || img.Height <= 0 {
		return 0
	}
	return img.Width * img.Height * bpp
}

// CurrentFrame returns the 1-based frame that should be showing at now.
//
// It is derived rather than ticked: playback state records which frame the
// animation was on and when it started, and the elapsed time says how far it
// has run since. Nothing has to wake up while an animation nobody renders is
// notionally playing.
func (img *KittyImage) CurrentFrame(now time.Time) int {
	total := img.FrameCount()
	current := img.Anim.Current
	if current < 1 || current > total {
		current = 1
	}
	if img.Anim.State == KittyAnimStopped || total < 2 || img.Anim.Started.IsZero() {
		return current
	}

	elapsed := now.Sub(img.Anim.Started).Milliseconds()
	if elapsed <= 0 {
		return current
	}

	// Walk forward frame by frame. A frame count is small (kitty caps
	// animations well below the point where this matters) and gaps vary per
	// frame, so there is no closed form worth the loss of clarity.
	cycle := int64(0)
	for n := 1; n <= total; n++ {
		cycle += int64(img.frameGap(n))
	}
	if cycle <= 0 {
		return current
	}

	loops := int64(0)
	for elapsed > 0 {
		gap := int64(img.frameGap(current))
		if elapsed < gap {
			break
		}
		elapsed -= gap
		current++
		if current > total {
			current = 1
			loops++
			// s=2 runs to the end and then waits for more frames; a finite
			// loop count stops on the last frame the same way.
			if img.Anim.State == KittyAnimWaiting ||
				(img.Anim.Loops > 0 && loops >= int64(img.Anim.Loops)) {
				return total
			}
		}
	}
	return current
}

// handleFrame implements a=f: transmit pixels into a frame of an existing
// image, either editing a frame that is already there (r=) or appending a new
// one built on a base frame (c=) or on a background colour (Y=).
func (h *KittyGraphicsHandler) handleFrame(cmd *KittyCommand) bool {
	if cmd.More {
		return h.handleChunkedFrame(cmd)
	}
	if pending := h.state.GetPending(); pending != nil && pending.Frame {
		if !h.state.AppendToPending(cmd.Data) {
			h.sendResponse(cmd, false, "EINVAL:transmission exceeds size limit")
			return true
		}
		full := h.state.FinalizeFrame()
		if full == nil {
			return true
		}
		return h.applyFrame(full, cmd)
	}
	return h.applyFrame(cmd, cmd)
}

// handleChunkedFrame accumulates an m=1 frame transmission. The parameters
// only ride on the first chunk, so they are held with the buffer.
func (h *KittyGraphicsHandler) handleChunkedFrame(cmd *KittyCommand) bool {
	pending := h.state.GetPending()
	if pending == nil {
		pending = &KittyPendingChunk{
			Frame:       true,
			Command:     *cmd,
			ImageID:     cmd.ImageID,
			ImageNumber: cmd.ImageNumber,
			Format:      cmd.Format,
			Medium:      cmd.Medium,
			Compression: cmd.Compression,
			Width:       cmd.Width,
			Height:      cmd.Height,
			DataBuffer:  make([]byte, 0, len(cmd.Data)*4),
		}
		pending.Command.Data = nil
		h.state.SetPending(pending)
	}
	if !h.state.AppendToPending(cmd.Data) {
		h.sendResponse(cmd, false, "EINVAL:transmission exceeds size limit")
	}
	return true
}

// applyFrame does the work of one complete a=f. params carries the frame
// parameters (from the first chunk of a chunked transmission), reply is the
// command the response should be addressed to.
func (h *KittyGraphicsHandler) applyFrame(params, reply *KittyCommand) bool {
	img := h.lookupImage(params)
	if img == nil {
		h.sendResponse(reply, false, "ENOENT:image not found")
		return true
	}
	bpp := bytesPerPixel(img.Format)
	if bpp == 0 {
		h.sendResponse(reply, false, "EINVAL:animation needs a raw pixel format")
		return true
	}

	data, ok := h.frameSource(params)
	if !ok {
		h.sendResponse(reply, false, "ENOENT:frame data not found")
		return true
	}

	srcW, srcH := params.Width, params.Height
	if srcW <= 0 {
		srcW = img.Width
	}
	if srcH <= 0 {
		srcH = img.Height
	}
	dstX, dstY := params.destX(), params.destY()
	if srcW <= 0 || srcH <= 0 || dstX < 0 || dstY < 0 ||
		dstX+srcW > img.Width || dstY+srcH > img.Height {
		h.sendResponse(reply, false, "EINVAL:frame rectangle out of bounds")
		return true
	}
	srcBPP := bytesPerPixel(params.Format)
	if srcBPP == 0 {
		srcBPP = bpp
	}
	if len(data) < srcW*srcH*srcBPP {
		h.sendResponse(reply, false, "EINVAL:frame data too short")
		return true
	}

	target := params.frameNumber()
	if target > img.FrameCount() {
		h.sendResponse(reply, false, "ENOENT:frame not found")
		return true
	}
	if target <= 0 {
		// A new frame starts as a copy of the base frame c, or as a flat fill
		// of the background colour Y when no base is named.
		base := params.baseFrame()
		var canvas []byte
		if base > 0 {
			src := img.frameData(base)
			if src == nil {
				h.sendResponse(reply, false, "ENOENT:base frame not found")
				return true
			}
			canvas = append([]byte(nil), src...)
		} else {
			canvas = fillFrame(img, params.BackgroundColor)
		}
		img.Frames = append(img.Frames, KittyFrame{Data: canvas, Gap: params.frameGap()})
		target = img.FrameCount()
	} else if gap := params.frameGap(); gap != 0 {
		img.setFrameGap(target, gap)
	}

	dst := img.frameData(target)
	if len(dst) < img.pixelBytes() {
		// A root frame transmitted as a short or non-raw payload cannot be
		// composed into; grow it to a full canvas first so the patch lands
		// somewhere well defined.
		grown := make([]byte, img.pixelBytes())
		copy(grown, dst)
		dst = grown
	}
	compositeRect(dst, img.Width, bpp, data, srcW, srcH, srcBPP,
		dstX, dstY, params.blendMode() == 1)
	img.setFrameData(target, dst)

	h.state.TouchImage(img)
	h.sendResponse(reply, true, "")
	return true
}

// frameSource resolves an a=f payload, which may arrive inline, through a
// file, or through shared memory, and may be zlib compressed.
func (h *KittyGraphicsHandler) frameSource(cmd *KittyCommand) ([]byte, bool) {
	data := cmd.Data
	switch cmd.Medium {
	case KittyMediumFile, KittyMediumTempFile:
		fileData, err := LoadFileData(cmd.FilePath)
		if err != nil {
			return nil, false
		}
		data = fileData
		if cmd.Medium == KittyMediumTempFile {
			removeTempTransmitFile(cmd.FilePath)
		}
	case KittyMediumSharedMemory:
		shmData, err := loadSharedMemory(cmd.FilePath, cmd.Size)
		if err != nil {
			return nil, false
		}
		data = shmData
	}
	if cmd.Compression == KittyCompressionZlib {
		decompressed, err := decompressZlib(data, decompressLimit(cmd.Width, cmd.Height))
		if err != nil {
			return nil, false
		}
		data = decompressed
	}
	return data, true
}

// fillFrame builds a frame-sized canvas of one RGBA colour.
func fillFrame(img *KittyImage, rgba uint32) []byte {
	n := img.pixelBytes()
	canvas := make([]byte, n)
	if rgba == 0 {
		return canvas
	}
	bpp := bytesPerPixel(img.Format)
	px := []byte{byte(rgba >> 24), byte(rgba >> 16), byte(rgba >> 8), byte(rgba)}
	for i := 0; i+bpp <= n; i += bpp {
		copy(canvas[i:i+bpp], px[:bpp])
	}
	return canvas
}

// compositeRect draws a srcW x srcH source rectangle into dst at (dstX, dstY).
// overwrite replaces the destination pixels; otherwise the source is alpha
// blended over them, which is what a=f means by X=0.
func compositeRect(dst []byte, dstW, dstBPP int, src []byte, srcW, srcH, srcBPP, dstX, dstY int, overwrite bool) {
	dstStride := dstW * dstBPP
	srcStride := srcW * srcBPP
	for row := range srcH {
		so := row * srcStride
		do := (dstY+row)*dstStride + dstX*dstBPP
		if do < 0 || do+srcW*dstBPP > len(dst) || so+srcStride > len(src) {
			return
		}
		if overwrite && srcBPP == dstBPP {
			copy(dst[do:do+srcStride], src[so:so+srcStride])
			continue
		}
		for col := range srcW {
			blendPixel(dst[do+col*dstBPP:], dstBPP, src[so+col*srcBPP:], srcBPP, overwrite)
		}
	}
}

// blendPixel writes one source pixel over one destination pixel. A source
// without an alpha channel is opaque.
func blendPixel(dst []byte, dstBPP int, src []byte, srcBPP int, overwrite bool) {
	alpha := 255
	if srcBPP == 4 {
		alpha = int(src[3])
	}
	if overwrite || alpha == 255 {
		copy(dst[:min(dstBPP, srcBPP)], src[:min(dstBPP, srcBPP)])
		if dstBPP == 4 && srcBPP == 3 {
			dst[3] = 255
		}
		return
	}
	if alpha == 0 {
		return
	}
	inv := 255 - alpha
	for i := range 3 {
		dst[i] = byte((int(src[i])*alpha + int(dst[i])*inv) / 255)
	}
	if dstBPP == 4 {
		dst[3] = byte(alpha + int(dst[3])*inv/255)
	}
}

// handleAnimation implements a=a: start, stop and step playback, and edit the
// gap of a frame without retransmitting it.
func (h *KittyGraphicsHandler) handleAnimation(cmd *KittyCommand) bool {
	img := h.lookupImage(cmd)
	if img == nil {
		h.sendResponse(cmd, false, "ENOENT:image not found")
		return true
	}

	// r= names a frame whose gap z= is being edited. This is the cheap way to
	// retime an animation, and it must not disturb playback.
	if n := cmd.frameNumber(); n > 0 {
		if n > img.FrameCount() {
			h.sendResponse(cmd, false, "ENOENT:frame not found")
			return true
		}
		if gap := cmd.frameGap(); gap != 0 {
			img.setFrameGap(n, gap)
		}
	}

	if n := cmd.baseFrame(); n > 0 {
		if n > img.FrameCount() {
			h.sendResponse(cmd, false, "ENOENT:frame not found")
			return true
		}
		img.Anim.Current = n
		img.Anim.Started = time.Now()
	}

	if loops := cmd.animLoops(); loops != 0 {
		// v=1 means play once and stop; the wire value is one more than the
		// number of repeats, and negative means loop forever.
		if loops < 0 {
			img.Anim.Loops = 0
		} else {
			img.Anim.Loops = loops
		}
	}

	switch cmd.animState() {
	case 1:
		// Freeze on whatever frame is showing now, so a stop does not rewind.
		img.Anim.Current = img.CurrentFrame(time.Now())
		img.Anim.State = KittyAnimStopped
	case 2:
		img.Anim.State = KittyAnimWaiting
		img.Anim.Started = time.Now()
	case 3:
		img.Anim.State = KittyAnimRunning
		img.Anim.Started = time.Now()
	}
	if img.Anim.Current < 1 {
		img.Anim.Current = 1
	}

	h.state.TouchImage(img)
	h.sendResponse(cmd, true, "")
	return true
}

// handleCompose implements a=c: copy a rectangle from one frame of an image
// into another frame of the same image, with no pixels on the wire at all.
func (h *KittyGraphicsHandler) handleCompose(cmd *KittyCommand) bool {
	img := h.lookupImage(cmd)
	if img == nil {
		h.sendResponse(cmd, false, "ENOENT:image not found")
		return true
	}
	bpp := bytesPerPixel(img.Format)
	if bpp == 0 {
		h.sendResponse(cmd, false, "EINVAL:animation needs a raw pixel format")
		return true
	}

	dstFrame, srcFrame := cmd.frameNumber(), cmd.baseFrame()
	if dstFrame <= 0 {
		dstFrame = 1
	}
	if srcFrame <= 0 {
		srcFrame = 1
	}
	dst, src := img.frameData(dstFrame), img.frameData(srcFrame)
	if dst == nil || src == nil {
		h.sendResponse(cmd, false, "ENOENT:frame not found")
		return true
	}

	w, hgt := cmd.composeWidth(), cmd.composeHeight()
	if w <= 0 {
		w = img.Width
	}
	if hgt <= 0 {
		hgt = img.Height
	}
	dstX, dstY := cmd.destX(), cmd.destY()
	srcX, srcY := cmd.composeSrcX(), cmd.composeSrcY()
	if dstX < 0 || dstY < 0 || srcX < 0 || srcY < 0 ||
		dstX+w > img.Width || dstY+hgt > img.Height ||
		srcX+w > img.Width || srcY+hgt > img.Height {
		h.sendResponse(cmd, false, "EINVAL:compose rectangle out of bounds")
		return true
	}
	if len(dst) < img.pixelBytes() || len(src) < img.pixelBytes() {
		h.sendResponse(cmd, false, "EINVAL:frame is not a full canvas")
		return true
	}

	// Composing a frame onto itself would read pixels this loop has already
	// written when the rectangles overlap, so the source is snapshotted.
	if dstFrame == srcFrame {
		src = append([]byte(nil), src...)
	}
	stride := img.Width * bpp
	overwrite := cmd.composeMode() == 1
	for row := range hgt {
		so := (srcY+row)*stride + srcX*bpp
		do := (dstY+row)*stride + dstX*bpp
		if overwrite {
			copy(dst[do:do+w*bpp], src[so:so+w*bpp])
			continue
		}
		for col := range w {
			blendPixel(dst[do+col*bpp:], bpp, src[so+col*bpp:], bpp, false)
		}
	}
	img.setFrameData(dstFrame, dst)

	h.state.TouchImage(img)
	h.sendResponse(cmd, true, "")
	return true
}

// lookupImage resolves the image a command addresses, by id or by number.
func (h *KittyGraphicsHandler) lookupImage(cmd *KittyCommand) *KittyImage {
	if cmd.ImageID > 0 {
		return h.state.GetImage(cmd.ImageID)
	}
	if cmd.ImageNumber > 0 {
		return h.state.GetImageByNumber(cmd.ImageNumber)
	}
	return nil
}
