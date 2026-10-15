package vt

import "os"

// KittyMediumIsFrame reports whether info describes the object that cmd, a
// shared memory (t=s) or temp file (t=t) transmission, hands to the terminal.
// Only then may a reader that did not create the object delete it.
//
// The name in a transmission is text the pane printed. Any program in the pane
// can print a name, including the name of an object another program still
// uses. So a name alone is no proof, and a delete needs all of these:
//   - the object is a regular file;
//   - this user owns it;
//   - its size is the size the command describes: O+S when S is given, or
//     O+s*v*3 for f=24 and O+s*v*4 for f=32 when it is not.
//
// A compressed (o=z) or PNG (f=100) frame without S has no size to compare,
// so it is never deleted.
func KittyMediumIsFrame(cmd *KittyCommand, info os.FileInfo) bool {
	if cmd == nil || info == nil || !info.Mode().IsRegular() {
		return false
	}
	if !kittyOwnedByMe(info) {
		return false
	}
	want, ok := kittyFrameSize(cmd)
	return ok && info.Size() == want
}

// kittyFrameSize is the size in bytes of the object cmd describes, and false
// when the command does not tell.
func kittyFrameSize(cmd *KittyCommand) (int64, bool) {
	if cmd.Offset < 0 || cmd.Size < 0 {
		return 0, false
	}
	if cmd.Size > 0 {
		return int64(cmd.Offset) + int64(cmd.Size), true
	}
	if cmd.Compression != KittyCompressionNone || cmd.Width <= 0 || cmd.Height <= 0 {
		return 0, false
	}
	var bpp int64
	switch cmd.Format {
	case KittyFormatRGB:
		bpp = 3
	case KittyFormatRGBA:
		bpp = 4
	default:
		return 0, false
	}
	return int64(cmd.Offset) + int64(cmd.Width)*int64(cmd.Height)*bpp, true
}
