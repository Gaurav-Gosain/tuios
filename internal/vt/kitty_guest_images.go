package vt

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// KittyImageNumberIDBase is where the ids given to images sent by number
// start. Kitty assigns the id itself when a guest transmits with I= and no i=,
// and the guest may name the image by that id later. Starting high keeps the
// ids clear of the small ones guests choose for themselves.
const KittyImageNumberIDBase uint32 = 1 << 31

// KittyGuestImages is what one pane's emulator knows about the images its
// guest named: which id each image number (I=) stands for, and the answer owed
// to an upload that is still arriving in chunks.
//
// It lives in the emulator because every emulator of a pane sees the same
// commands in the same order: the daemon's, and each attached client's. The
// ids it gives are a function of that order alone, so they agree wherever
// they are computed. The answers it writes go out through the emulator's
// response pipe. Only one of those pipes reaches the pane: the daemon's when
// there is a daemon, which client emulators drain, and the client's when it
// runs alone. So a guest gets one answer however many clients are attached.
//
// The zero value is ready to use.
type KittyGuestImages struct {
	numbers map[uint32]uint32
	next    uint32
	// guestHigh holds the ids at or above KittyImageNumberIDBase that the
	// guest chose itself with i=, so a number is never given one of them.
	guestHigh map[uint32]struct{}
	pending   *kittyUploadReply
}

// kittyUploadReply is the answer owed to an upload that named its image.
type kittyUploadReply struct {
	id, number, placement uint32
	quiet                 int
}

// Handle resolves a command's image number and answers it if it is the last
// chunk of an upload that named its image. write sends an answer to the
// guest. It reports false when the command names a number no image was sent
// under, which kitty answers with ENOENT and draws nothing for, so the caller
// drops the command.
func (g *KittyGuestImages) Handle(cmd *KittyCommand, write func([]byte)) bool {
	if cmd == nil || IsKittyEchoedResponse(cmd) {
		return true
	}
	if cmd.PayloadErr != nil {
		// Answered with EINVAL already. The upload it was part of is over.
		g.pending = nil
		return true
	}
	if !g.Resolve(cmd) {
		return false
	}
	if reply := g.uploadReply(cmd); reply != nil && write != nil {
		write(reply)
	}
	return true
}

// Resolve turns a command that names its image by number (I=) and not by id
// (i=) into one that names it by id. It sets ImageID, turns d=n and d=N into
// d=i and d=I, and reports false when the number names no image.
//
// Every transmission under a number is a new image with a new id, as in
// kitty, and the number names the newest of them from then on. The id the
// number named before is left in ReplacedImageID.
func (g *KittyGuestImages) Resolve(cmd *KittyCommand) bool {
	if cmd.ImageID != 0 {
		if cmd.ImageID >= KittyImageNumberIDBase && isKittyUpload(cmd) {
			if g.guestHigh == nil {
				g.guestHigh = make(map[uint32]struct{})
			}
			g.guestHigh[cmd.ImageID] = struct{}{}
		}
		return true
	}
	if cmd.ImageNumber == 0 {
		// d=A frees every image, so no number names one after it.
		if cmd.Action == KittyActionDelete && cmd.Delete == 'A' {
			g.numbers = nil
		}
		return true
	}
	number := cmd.ImageNumber

	switch cmd.Action {
	case KittyActionTransmit, KittyActionTransmitPlace:
		if g.numbers == nil {
			g.numbers = make(map[uint32]uint32)
		}
		cmd.ReplacedImageID = g.numbers[number]
		id := g.allocate()
		g.numbers[number] = id
		cmd.ImageID = id

	case KittyActionDelete:
		// Only d=n and d=N name an image by number. Every other target
		// ignores I=.
		if cmd.Delete != KittyDeleteByNumber && cmd.Delete != KittyDeleteByNumberPlacement {
			return true
		}
		id, ok := g.numbers[number]
		if !ok {
			return false
		}
		cmd.ImageID = id
		if cmd.Delete == KittyDeleteByNumber {
			cmd.Delete = KittyDeleteByID
		} else {
			cmd.Delete = KittyDeleteByIDAndPlacement
			// d=N frees the image's data, after which the number names
			// nothing. With p= it deletes that one placement, and the
			// image stays while its other placements remain.
			if cmd.PlacementID == 0 {
				delete(g.numbers, number)
			}
		}

	case KittyActionQuery:
		return true

	default:
		id, ok := g.numbers[number]
		if !ok {
			return false
		}
		cmd.ImageID = id
	}
	return true
}

// allocate picks the next id for an image sent by number.
func (g *KittyGuestImages) allocate() uint32 {
	for {
		if g.next < KittyImageNumberIDBase {
			g.next = KittyImageNumberIDBase
		}
		id := g.next
		g.next++
		if _, used := g.guestHigh[id]; !used {
			return id
		}
	}
}

func isKittyUpload(cmd *KittyCommand) bool {
	return cmd.Action == KittyActionTransmit || cmd.Action == KittyActionTransmitPlace
}

// uploadReply is the answer kitty gives an upload once its last chunk is in:
// i=<id>, with I=<n> and p=<p> when the guest gave them, then OK or an error.
// q=1 suppresses an OK and q=2 every answer. An upload that names no image
// gets none. For an image sent by number, this answer is the only way the
// guest learns its id.
func (g *KittyGuestImages) uploadReply(cmd *KittyCommand) []byte {
	if !isKittyUpload(cmd) {
		return nil
	}
	var r kittyUploadReply
	if cmd.ImageID != 0 {
		r = kittyUploadReply{id: cmd.ImageID, number: cmd.ImageNumber, placement: cmd.PlacementID, quiet: cmd.Quiet}
		if cmd.More {
			g.pending = &r
			return nil
		}
	} else {
		if cmd.More || g.pending == nil {
			return nil
		}
		r = *g.pending
	}
	g.pending = nil

	msg := "OK"
	if failed := kittyUploadFileError(cmd); failed != "" {
		msg = failed
	}
	if r.quiet >= 2 || (r.quiet >= 1 && msg == "OK") {
		return nil
	}
	reply := fmt.Appendf(nil, "\x1b_Gi=%d", r.id)
	if r.number != 0 {
		reply = fmt.Appendf(reply, ",I=%d", r.number)
	}
	if r.placement != 0 {
		reply = fmt.Appendf(reply, ",p=%d", r.placement)
	}
	reply = append(reply, ';')
	reply = append(reply, msg...)
	return append(reply, "\x1b\\"...)
}

// kittyUploadFileError checks that the file a t=f or t=t upload names can be
// read, and returns kitty's error for it, or "" when it can. The file is
// opened and closed, not read: the host terminal or the client reads it, and
// a temporary file is theirs to delete once read. A path that is not a
// regular file is refused before it is opened, because opening a FIFO or a
// device can block or have side effects.
//
// A shared memory upload (t=s) is not checked: its name is a path only on
// Linux.
func kittyUploadFileError(cmd *KittyCommand) string {
	if cmd.Medium != KittyMediumFile && cmd.Medium != KittyMediumTempFile {
		return ""
	}
	path := cmd.FilePath
	if path == "" || !filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		return "EINVAL:the file path is not an absolute path"
	}
	info, err := os.Stat(path)
	if err == nil && !info.Mode().IsRegular() {
		return "EINVAL:the file is not a regular file"
	}
	if err == nil {
		var f *os.File
		if f, err = os.Open(path); err == nil {
			_ = f.Close()
			return ""
		}
	}
	if errors.Is(err, fs.ErrNotExist) {
		return "ENOENT:Failed to open file for graphics transmission: no such file"
	}
	return "EBADF:Failed to open file for graphics transmission: " + kittyErrText(err)
}

// kittyErrText is an error's text without the path, which a reply has no
// room for and the guest already knows.
func kittyErrText(err error) string {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		err = pathErr.Err
	}
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == ';' {
			return ' '
		}
		return r
	}, err.Error())
}
