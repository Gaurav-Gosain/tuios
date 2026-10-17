package app

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"

	"tuios.dev/vt"
)

// Keeping a kitty image for a screenshot.
//
// A kitty image is drawn by the host terminal, not by tuios, so the pixels are
// not in any cell a screenshot reads. A screenshot that is to show the picture
// needs the bytes the guest sent, and the passthrough normally lets go of
// them as soon as they are on their way to the host.
//
// So the passthrough keeps the last payload of each image it forwards: the
// bytes as the guest sent them, still encoded. It keeps a reference to a
// buffer it already holds wherever it can, so keeping costs no copy and no
// work per frame, only the memory of one payload per image, under
// maxKeptImageBytes for the whole connection. Every client that shows images
// pays that memory, whether or not it ever takes a screenshot, so the bound
// is small and the images least likely to be in a screenshot go first. Nothing is decoded until a
// screenshot asks, and a screenshot decodes only the images it shows (see
// screenshot_images.go).
//
// Two cases cost more than a reference:
//   - A temporary file or shared memory object (t=t, t=s) forwarded to a local
//     host is read once, when it is a new image. The host deletes it after it
//     reads it, so there is no later chance. A stream of frames under one id
//     is not read: the entry is dropped instead, and the capture leaves that
//     image out rather than show a frame it is not showing.
//   - A file (t=f) is not read at all. Its path is kept and read when a
//     screenshot asks, after the checks kittyMediumPath made are made again,
//     since the pane can swap the file for something else in between.
//
// An image the guest sends in chunks without placing it (a=t with m=1) is
// forwarded chunk by chunk and never whole, so it is not kept.

// maxKeptImageBytes bounds the payloads one connection keeps. Past it entries
// are dropped, those with no placement on the host first and then the oldest,
// and a screenshot leaves those images out. A screen of PNGs fits in it with
// room to spare. A single image larger than it is not kept. A payload counts
// at its capacity: a buffer grown chunk by chunk holds more than its length.
const maxKeptImageBytes = 8 << 20

// keptImage is the last payload of one image. It is not changed after it is
// made: a new transmission makes a new entry, so a screenshot can read one
// off the passthrough's lock.
type keptImage struct {
	guestID     uint32
	format      vt.KittyGraphicsFormat
	compression vt.KittyGraphicsCompression
	width       int // declared s=, or 0
	height      int // declared v=, or 0
	data        []byte
	// path is a file to read when a screenshot asks, with offset and size
	// as the guest gave them. Set instead of data.
	path         string
	offset, size int
	seq          uint64
}

// virtualBox is the box of cells one virtual placement fills.
type virtualBox struct {
	cols, rows int
	z          int32
}

// keepImage records the payload of an image the host is being sent. The
// caller holds kp.mu.
func (kp *KittyPassthrough) keepImage(windowID string, hostID uint32, k *keptImage) {
	if hostID == 0 {
		return
	}
	if k == nil || (len(k.data) == 0 && k.path == "") || cap(k.data) > maxKeptImageBytes {
		kp.forgetKept(windowID, hostID)
		return
	}
	if kp.kept == nil {
		kp.kept = make(map[string]map[uint32]*keptImage)
	}
	if kp.kept[windowID] == nil {
		kp.kept[windowID] = make(map[uint32]*keptImage)
	}
	if old := kp.kept[windowID][hostID]; old != nil {
		kp.keptBytes -= cap(old.data)
	}
	kp.keptSeq++
	k.seq = kp.keptSeq
	kp.kept[windowID][hostID] = k
	kp.keptBytes += cap(k.data)
	for kp.keptBytes > maxKeptImageBytes {
		kp.evictOldestKept()
	}
}

// evictOldestKept drops the entry kept longest ago among those the host is
// not showing, or the oldest of all when it shows every one. An image
// scrolled out of view or deleted from the screen is less likely to be in a
// screenshot than one on it.
func (kp *KittyPassthrough) evictOldestKept() {
	var oldWin string
	var oldID uint32
	var oldSeq uint64
	oldShown := true
	for win, ids := range kp.kept {
		for id, k := range ids {
			if len(k.data) == 0 {
				continue // a path costs nothing to keep
			}
			// The entry just kept has no placement yet: the record is made
			// after the payload. It is the one most likely to be on screen.
			shown := k.seq == kp.keptSeq || kp.keptShown(win, id)
			if oldSeq == 0 || (oldShown && !shown) || (shown == oldShown && k.seq < oldSeq) {
				oldWin, oldID, oldSeq, oldShown = win, id, k.seq, shown
			}
		}
	}
	if oldSeq == 0 {
		kp.keptBytes = 0
		return
	}
	kp.forgetKept(oldWin, oldID)
}

// keptShown reports whether the host shows any placement of an image now,
// or the image has a virtual placement, whose cells may be on screen.
func (kp *KittyPassthrough) keptShown(windowID string, hostID uint32) bool {
	if len(kp.virtualBoxes[windowID][hostID]) > 0 {
		return true
	}
	ix := kp.placeIdx[windowID]
	if ix == nil {
		return false
	}
	for slot := range ix.byHost[hostID] {
		if p := kp.placements[windowID][placementKey{hostID, slot}]; p != nil && !p.Hidden {
			return true
		}
	}
	return false
}

// forgetKept drops one image's entry and its virtual boxes, or those of every
// image of a window when hostID is 0. The boxes go even where no payload was
// kept: a virtual placement of an image sent in chunks records a box too.
// The caller holds kp.mu.
func (kp *KittyPassthrough) forgetKept(windowID string, hostID uint32) {
	if hostID == 0 {
		for _, k := range kp.kept[windowID] {
			kp.keptBytes -= cap(k.data)
		}
		delete(kp.kept, windowID)
		delete(kp.virtualBoxes, windowID)
		return
	}
	if boxes := kp.virtualBoxes[windowID]; boxes != nil {
		delete(boxes, hostID)
		if len(boxes) == 0 {
			delete(kp.virtualBoxes, windowID)
		}
	}
	ids := kp.kept[windowID]
	if k := ids[hostID]; k != nil {
		kp.keptBytes -= cap(k.data)
		delete(ids, hostID)
		if len(ids) == 0 {
			delete(kp.kept, windowID)
		}
	}
}

// forgetUnnamedKept drops the entry of an image nothing can show again: one
// sent under the auto-assign id, so no later a=p can name it, with no
// placement left. The caller holds kp.mu.
func (kp *KittyPassthrough) forgetUnnamedKept(windowID string, hostID uint32) {
	k := kp.kept[windowID][hostID]
	if k == nil || k.guestID != 0 || kp.hostHasPlacement(windowID, hostID) {
		return
	}
	if len(kp.virtualBoxes[windowID][hostID]) > 0 {
		return
	}
	kp.forgetKept(windowID, hostID)
}

// forgetUnnamedWindowKept is forgetUnnamedKept for every image of a window
// whose placements were all dropped.
func (kp *KittyPassthrough) forgetUnnamedWindowKept(windowID string) {
	for hostID := range kp.kept[windowID] {
		kp.forgetUnnamedKept(windowID, hostID)
	}
}

// keepVirtualBox records the box a virtual placement fills, which is what
// placeholder cells are cut from.
func (kp *KittyPassthrough) keepVirtualBox(windowID string, hostID, placementID uint32, cols, rows int, z int32) {
	if kp.virtualBoxes == nil {
		kp.virtualBoxes = make(map[string]map[uint32]map[uint32]virtualBox)
	}
	if kp.virtualBoxes[windowID] == nil {
		kp.virtualBoxes[windowID] = make(map[uint32]map[uint32]virtualBox)
	}
	boxes := kp.virtualBoxes[windowID][hostID]
	if boxes == nil {
		boxes = make(map[uint32]virtualBox)
		kp.virtualBoxes[windowID][hostID] = boxes
	}
	if _, ok := boxes[placementID]; !ok && len(boxes) >= maxPlacementsPerImage {
		return
	}
	boxes[placementID] = virtualBox{cols: cols, rows: rows, z: z}
}

// keptFromCommand is the entry for a transmission whose first command is cmd
// and whose decoded payload is data.
func keptFromCommand(cmd *vt.KittyCommand, data []byte) *keptImage {
	return &keptImage{
		guestID:     cmd.ImageID,
		format:      cmd.Format,
		compression: cmd.Compression,
		width:       cmd.Width,
		height:      cmd.Height,
		data:        data,
	}
}

// keepFileMedium keeps the image a file medium transmission names, on the
// path that forwards the name to a local host. path is what kittyMediumPath
// resolved. newImage says the id is not a stream that already has a frame.
func (kp *KittyPassthrough) keepFileMedium(cmd *vt.KittyCommand, windowID string, hostID uint32, path string, newImage bool) {
	if hostID == 0 {
		return
	}
	if path == "" {
		// Shared memory on macOS has no path to read.
		kp.forgetKept(windowID, hostID)
		return
	}
	k := keptFromCommand(cmd, nil)
	if cmd.Medium == vt.KittyMediumFile {
		k.path, k.offset, k.size = path, cmd.Offset, cmd.Size
		kp.keepImage(windowID, hostID, k)
		return
	}
	if !newImage {
		kp.forgetKept(windowID, hostID)
		return
	}
	data, err := readKittyMedium(path, cmd.Offset, cmd.Size, maxKeptImageBytes, false)
	if err != nil {
		kp.forgetKept(windowID, hostID)
		return
	}
	k.data = data
	kp.keepImage(windowID, hostID, k)
}

// readKittyMedium reads the bytes a file medium names: size bytes from
// offset, or the rest of the file when size is 0. path is what
// kittyMediumPath resolved, so it has no symlink in it. It refuses anything
// that is not a regular file and anything larger than limit.
//
// The pane can change the file between the check and the read: put a symlink
// or a named pipe in its place. The open follows no symlink in the last part
// of the path and does not wait for a writer, and the open file is checked
// again. With recheck set, which is for a path kept since an earlier frame,
// the whole path is resolved again, and the file must still be the pane's
// own and outside the places kitty refuses.
func readKittyMedium(path string, offset, size, limit int, recheck bool) ([]byte, error) {
	if recheck {
		if resolved, err := filepath.EvalSymlinks(path); err != nil || resolved != path || vt.KittySensitivePath(path) {
			return nil, errors.New("the path changed")
		}
	}
	f, err := os.OpenFile(path, os.O_RDONLY|openNonblock|openNoFollow, 0) // #nosec G304 - checked by kittyMediumPath
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || (recheck && !ownedByMe(info)) {
		return nil, errors.New("not a regular file")
	}
	n := info.Size() - int64(max(offset, 0))
	if size > 0 {
		n = min(n, int64(size))
	}
	if n <= 0 || n > int64(limit) {
		return nil, fmt.Errorf("%d bytes is outside the limit", n)
	}
	if offset > 0 {
		if _, err := f.Seek(int64(offset), io.SeekStart); err != nil {
			return nil, err
		}
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(f, data); err != nil {
		return nil, err
	}
	return data, nil
}

// errImageTooLarge says a picture would take more pixels than are left in a
// screenshot's budget.
var errImageTooLarge = errors.New("image is larger than the screenshot budget")

// decode turns the payload into pixels, refusing any image larger than
// maxPixels. Every size in the payload is the guest's to choose, so each is
// checked before anything is allocated for it.
func (k *keptImage) decode(maxPixels int) (image.Image, error) {
	data := k.data
	if k.path != "" {
		var err error
		data, err = readKittyMedium(k.path, k.offset, k.size, maxPassthroughTransmitBytes, true)
		if err != nil {
			return nil, err
		}
	}
	switch k.format {
	case vt.KittyFormatPNG:
		if k.compression == vt.KittyCompressionZlib {
			var err error
			data, err = inflateBounded(data, maxPassthroughTransmitBytes)
			if err != nil {
				return nil, err
			}
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > maxPixels/cfg.Height {
			return nil, errImageTooLarge
		}
		return png.Decode(bytes.NewReader(data))
	case vt.KittyFormatRGB, vt.KittyFormatRGBA, 0:
		bpp := 4
		if k.format == vt.KittyFormatRGB {
			bpp = 3
		}
		w, h := k.width, k.height
		if w <= 0 || h <= 0 || w > maxPixels/h {
			return nil, errImageTooLarge
		}
		want := w * h * bpp
		if k.compression == vt.KittyCompressionZlib {
			var err error
			data, err = inflateBounded(data, want)
			if err != nil {
				return nil, err
			}
		}
		if len(data) < want {
			return nil, fmt.Errorf("payload has %d bytes, the image needs %d", len(data), want)
		}
		if bpp == 4 {
			// Kitty's RGBA is not premultiplied, which is what NRGBA is.
			return &image.NRGBA{Pix: data[:want:want], Stride: w * 4, Rect: image.Rect(0, 0, w, h)}, nil
		}
		out := image.NewNRGBA(image.Rect(0, 0, w, h))
		for i, j := 0, 0; i < want; i, j = i+3, j+4 {
			out.Pix[j], out.Pix[j+1], out.Pix[j+2], out.Pix[j+3] = data[i], data[i+1], data[i+2], 255
		}
		return out, nil
	}
	return nil, fmt.Errorf("unknown kitty image format %d", k.format)
}

// pixels is the image's area as the guest declared it, 0 when it did not.
func (k *keptImage) pixels() int {
	if k.width <= 0 || k.height <= 0 {
		return 0
	}
	return k.width * k.height
}

// inflateBounded undoes zlib, refusing output past limit, so a small payload
// cannot inflate to fill memory.
func inflateBounded(data []byte, limit int) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()
	out, err := io.ReadAll(io.LimitReader(r, int64(limit)+1))
	if err != nil {
		return nil, err
	}
	if len(out) > limit {
		return nil, errImageTooLarge
	}
	return out, nil
}
