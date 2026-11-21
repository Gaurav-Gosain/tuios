package app

import (
	"bytes"
	"compress/zlib"
	"errors"
	"image"
	"image/png"
	"io"
	"path/filepath"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
	"tuios.dev/vt"
)

const maxKeptImageBytes = 8 << 20

type keptImage struct {
	guestID      uint32
	format       vt.KittyGraphicsFormat
	compression  vt.KittyGraphicsCompression
	width        int
	height       int
	data         []byte
	path         string
	offset, size int
	seq          uint64
}

type virtualBox struct {
	cols, rows int
	z          int32
}

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

func (kp *KittyPassthrough) evictOldestKept() {
	var oldWin string
	var oldID uint32
	var oldSeq uint64
	oldShown := true
	for win, ids := range kp.kept {
		for id, k := range ids {
			if len(k.data) == 0 {
				continue
			}
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

func (kp *KittyPassthrough) forgetUnnamedWindowKept(windowID string) {
	for hostID := range kp.kept[windowID] {
		kp.forgetUnnamedKept(windowID, hostID)
	}
}

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

func (kp *KittyPassthrough) keepFileMedium(cmd *vt.KittyCommand, windowID string, hostID uint32, path string, newImage bool) {
	if hostID == 0 {
		return
	}
	if path == "" {
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

func readKittyMedium(path string, offset, size, limit int, recheck bool) ([]byte, error) {
	f, n, err := openKittyMedium(path, offset, size, recheck)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	if n > int64(limit) {
		return nil, errfmt.Errorf("%d bytes is outside the limit", n)
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(f, data); err != nil {
		return nil, err
	}
	return data, nil
}

func kittyMediumPNGSize(path string, offset, size int, zlibbed bool) (w, h int, ok bool) {
	f, n, err := openKittyMedium(path, offset, size, false)
	if err != nil {
		return 0, 0, false
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, min(n, 1024))
	if _, err := io.ReadFull(f, head); err != nil {
		return 0, 0, false
	}
	return pngSize(head, zlibbed)
}

func openKittyMedium(path string, offset, size int, recheck bool) (kittyObject, int64, error) {
	if recheck {
		if resolved, err := filepath.EvalSymlinks(path); err != nil || resolved != path || vt.KittySensitivePath(path) {
			return nil, 0, errors.New("the path changed")
		}
	}
	f, err := openKittyObject(path, openNonblock|openNoFollow)
	if err != nil {
		return nil, 0, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, 0, err
	}
	if !info.Mode().IsRegular() || (recheck && !ownedByMe(info)) {
		_ = f.Close()
		return nil, 0, errors.New("not a regular file")
	}
	n := info.Size() - int64(max(offset, 0))
	if size > 0 {
		n = min(n, int64(size))
	}
	if n <= 0 {
		_ = f.Close()
		return nil, 0, errfmt.Errorf("%d bytes is outside the file", n)
	}
	if offset > 0 {
		if _, err := f.Seek(int64(offset), io.SeekStart); err != nil {
			_ = f.Close()
			return nil, 0, err
		}
	}
	return f, n, nil
}

var errImageTooLarge = errors.New("image is larger than the screenshot budget")

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
			return nil, errfmt.Errorf("payload has %d bytes, the image needs %d", len(data), want)
		}
		if bpp == 4 {
			return &image.NRGBA{Pix: data[:want:want], Stride: w * 4, Rect: image.Rect(0, 0, w, h)}, nil
		}
		out := image.NewNRGBA(image.Rect(0, 0, w, h))
		for i, j := 0, 0; i < want; i, j = i+3, j+4 {
			out.Pix[j], out.Pix[j+1], out.Pix[j+2], out.Pix[j+3] = data[i], data[i+1], data[i+2], 255
		}
		return out, nil
	}
	return nil, errfmt.Errorf("unknown kitty image format %d", k.format)
}

func (k *keptImage) pixels() int {
	if k.width <= 0 || k.height <= 0 {
		return 0
	}
	return k.width * k.height
}

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
