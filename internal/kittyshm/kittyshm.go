// Package kittyshm reads and deletes the shared memory objects a kitty graphics
// command names (t=s) on a machine where such an object has no path.
//
// On Linux an object is a file under /dev/shm, and the ordinary file calls
// reach it. On macOS it is only a name: it is opened with shm_open, deleted
// with shm_unlink, and a read of the descriptor fails, so its bytes are reached
// through a mapping. Both calls are plain system calls, and Go reaches them
// without cgo. They are kernel numbers, not libSystem, so Available tries them
// before tuios offers the medium to anyone.
//
// The rules a frame has to pass before it is deleted are the ones
// vt.KittyMediumIsFrame applies to a file. The name is text the pane printed.
package kittyshm

import (
	"errors"
	"io"

	"tuios.dev/vt"
)

var _ io.ReadSeekCloser = (*Object)(nil)

// ErrTooLarge is an object past the limit Open was given. It is not mapped.
var ErrTooLarge = errors.New("the shared memory object is too large")

// FrameBytes is the size in bytes of the frame cmd describes, and false when
// the command does not tell. It is the rule of vt.KittyMediumIsFrame.
func FrameBytes(cmd *vt.KittyCommand) (int64, bool) {
	if cmd.Offset < 0 || cmd.Size < 0 {
		return 0, false
	}
	if cmd.Size > 0 {
		return int64(cmd.Offset) + int64(cmd.Size), true
	}
	if cmd.Compression != vt.KittyCompressionNone || cmd.Width <= 0 || cmd.Height <= 0 {
		return 0, false
	}
	var bpp int64
	switch cmd.Format {
	case vt.KittyFormatRGB:
		bpp = 3
	case vt.KittyFormatRGBA:
		bpp = 4
	default:
		return 0, false
	}
	return int64(cmd.Offset) + int64(cmd.Width)*int64(cmd.Height)*bpp, true
}

// IsFrame reports whether an object of size bytes holds the frame cmd describes.
// The system rounds an object's size up to whole pages, so the comparison rounds
// the frame's size the same way.
func IsFrame(cmd *vt.KittyCommand, size int64, regular, owned bool, pageSize int64) bool {
	if cmd == nil || !regular || !owned || pageSize <= 0 {
		return false
	}
	want, ok := FrameBytes(cmd)
	if !ok {
		return false
	}
	return size == (want+pageSize-1)/pageSize*pageSize
}
