package vt

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"maps"
	"slices"
	"time"
)

const kittyImageNumberIDBase uint32 = 1 << 31

const maxKittyKnownImages = 1 << 16

const maxKittyFileChecks = 4

var kittyFileCheckTimeout = 2 * time.Second

var kittyFileCheck = kittyUploadFileError

// KittyGuestImages is what one pane's emulator knows about the images its
// guest named: which id each image number (I=) stands for, which ids hold an
// image, and the answer owed to an upload that is still arriving in chunks.
//
// It lives in the emulator because every emulator of a pane sees the same
// commands in the same order: the daemon's, and each attached client's. The
// ids it gives follow from that order. A client that attaches later has not
// seen the commands before it attached, so it takes the daemon's record with
// the snapshot it restores (see KittyGuestImagesState). From there it gives
// the same ids as the daemon.
//
// The answers go out through the emulator's response pipe, and only the
// emulator that is set to answer writes them (SetKittyUploadAnswers). A
// daemon's emulator answers. A client emulator of a daemon pane does not,
// and its pipe is drained anyway, so a guest gets one answer however many
// clients are attached.
//
// The zero value answers and is ready to use.
type KittyGuestImages struct {
	numbers   map[uint32]uint32
	known     map[uint32]struct{}
	next      uint32
	guestHigh map[uint32]struct{}
	pending   *kittyUploadReply

	silent         bool
	filesElsewhere bool
	checks         chan struct{}
}

// KittyGuestImagesState is the part of KittyGuestImages a client takes from
// the daemon when it attaches, so both give an image number the same id.
type KittyGuestImagesState struct {
	Numbers   map[uint32]uint32 `json:"numbers,omitempty"`    // image number to the id it stands for
	Known     []uint32          `json:"known,omitempty"`      // ids that hold an image
	Next      uint32            `json:"next,omitempty"`       // next id to give an image sent by number
	GuestHigh []uint32          `json:"guest_high,omitempty"` // ids at or above kittyImageNumberIDBase the guest chose itself
}

// State returns a copy of what a late client needs.
func (g *KittyGuestImages) State() *KittyGuestImagesState {
	if len(g.numbers) == 0 && len(g.known) == 0 && g.next == 0 {
		return nil
	}
	return &KittyGuestImagesState{
		Numbers:   maps.Clone(g.numbers),
		Known:     slices.Sorted(maps.Keys(g.known)),
		Next:      g.next,
		GuestHigh: slices.Sorted(maps.Keys(g.guestHigh)),
	}
}

// Restore replaces the record with the daemon's. A nil state, from a daemon
// that sends none, leaves the record as it is.
func (g *KittyGuestImages) Restore(s *KittyGuestImagesState) {
	if s == nil {
		return
	}
	g.numbers = maps.Clone(s.Numbers)
	g.known = make(map[uint32]struct{}, len(s.Known))
	for _, id := range s.Known {
		g.known[id] = struct{}{}
	}
	g.next = s.Next
	g.guestHigh = make(map[uint32]struct{}, len(s.GuestHigh))
	for _, id := range s.GuestHigh {
		g.guestHigh[id] = struct{}{}
	}
	g.pending = nil
}

type kittyUploadReply struct {
	id, number, placement uint32
	quiet                 int
	failed                string
}

// Handle resolves a command's image number, keeps the record of which ids
// hold an image, and answers what kitty answers: the last chunk of an upload
// that named its image, and a placement. write sends an answer to the guest.
// It reports false when the command must not reach the passthrough: it names
// both i= and I=, or a number or an image the pane does not have.
func (g *KittyGuestImages) Handle(cmd *KittyCommand, write func([]byte)) bool {
	if cmd == nil || IsKittyEchoedResponse(cmd) {
		return true
	}
	if g.silent {
		write = nil
	}
	if cmd.PayloadErr != nil {
		g.pending = nil
		return true
	}
	if cmd.ImageID != 0 && cmd.ImageNumber != 0 {
		g.pending = nil
		g.send(write, kittyReplyOf(cmd), "EINVAL:Must not specify both image id and image number")
		return false
	}

	upload := isKittyUpload(cmd)
	if upload && g.pending != nil &&
		(cmd.ImageID == 0 || cmd.ImageID == g.pending.id) &&
		(cmd.ImageNumber == 0 || cmd.ImageNumber == g.pending.number) {
		return g.continueUpload(cmd, write)
	}
	if upload {
		g.pending = nil
	}

	if !g.resolve(cmd) {
		if cmd.Action == KittyActionPlace {
			g.send(write, kittyReplyOf(cmd), "ENOENT:Put command refers to a non-existent image")
		}
		return false
	}

	switch {
	case upload:
		g.startUpload(cmd, write)
	case cmd.Action == KittyActionPlace && cmd.ImageID != 0:
		if _, ok := g.known[cmd.ImageID]; !ok {
			g.send(write, kittyReplyOf(cmd), "ENOENT:Put command refers to a non-existent image")
			return false
		}
		g.send(write, kittyReplyOf(cmd), "OK")
	case cmd.Action == KittyActionDelete:
		g.forget(cmd)
	}
	return true
}

// SetAnswers says whether this record answers the guest. Off, it still gives
// ids, and it opens no file.
func (g *KittyGuestImages) SetAnswers(on bool) { g.silent = !on }

// SetFilesElsewhere says the pane runs on another machine. Its file medium
// uploads are refused without a look at the file.
func (g *KittyGuestImages) SetFilesElsewhere(on bool) { g.filesElsewhere = on }

func (g *KittyGuestImages) resolve(cmd *KittyCommand) bool {
	if cmd.ImageID != 0 {
		if cmd.ImageID >= kittyImageNumberIDBase && isKittyUpload(cmd) {
			if g.guestHigh == nil {
				g.guestHigh = make(map[uint32]struct{})
			}
			g.guestHigh[cmd.ImageID] = struct{}{}
		}
		return true
	}
	if cmd.ImageNumber == 0 {
		return true
	}
	number := cmd.ImageNumber

	switch cmd.Action {
	case KittyActionTransmit, KittyActionTransmitPlace:
		if g.numbers == nil {
			g.numbers = make(map[uint32]uint32)
		}
		id := g.allocate()
		g.numbers[number] = id
		cmd.ImageID = id

	case KittyActionDelete:
		if cmd.Delete != KittyDeleteByNumber && cmd.Delete != KittyDeleteByNumberFree {
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
			cmd.Delete = KittyDeleteByIDFree
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

func (g *KittyGuestImages) forget(cmd *KittyCommand) {
	switch cmd.Delete {
	case KittyDeleteAllFree:
		g.numbers = nil
		g.known = nil
	case KittyDeleteByIDFree:
		if cmd.PlacementID == 0 {
			delete(g.known, cmd.ImageID)
		}
	}
}

func (g *KittyGuestImages) allocate() uint32 {
	for {
		if g.next < kittyImageNumberIDBase {
			g.next = kittyImageNumberIDBase
		}
		id := g.next
		g.next++
		if _, used := g.guestHigh[id]; !used {
			return id
		}
	}
}

func (g *KittyGuestImages) know(id uint32) {
	if g.known == nil {
		g.known = make(map[uint32]struct{})
	}
	if len(g.known) >= maxKittyKnownImages {
		for old := range g.known {
			delete(g.known, old)
			break
		}
	}
	g.known[id] = struct{}{}
}

func isKittyUpload(cmd *KittyCommand) bool {
	return cmd.Action == KittyActionTransmit || cmd.Action == KittyActionTransmitPlace
}

func kittyReplyOf(cmd *KittyCommand) kittyUploadReply {
	return kittyUploadReply{id: cmd.ImageID, number: cmd.ImageNumber, placement: cmd.PlacementID, quiet: cmd.Quiet}
}

func (g *KittyGuestImages) startUpload(cmd *KittyCommand, write func([]byte)) {
	if cmd.ImageID == 0 {
		return
	}
	r := kittyReplyOf(cmd)
	r.failed = kittyPNGHeadError(cmd)
	g.know(cmd.ImageID)
	if cmd.More {
		g.pending = &r
		return
	}
	g.finishUpload(cmd, r, write)
}

func (g *KittyGuestImages) continueUpload(cmd *KittyCommand, write func([]byte)) bool {
	r := *g.pending
	if cmd.ImageNumber != 0 {
		cmd.ImageID = r.id
	}
	r.quiet = max(r.quiet, cmd.Quiet)
	if cmd.More {
		*g.pending = r
		return true
	}
	g.pending = nil
	g.finishUpload(cmd, r, write)
	return true
}

func (g *KittyGuestImages) finishUpload(cmd *KittyCommand, r kittyUploadReply, write func([]byte)) {
	if r.failed != "" {
		g.send(write, r, r.failed)
		return
	}
	if cmd.Medium != KittyMediumFile && cmd.Medium != KittyMediumTempFile {
		g.send(write, r, "OK")
		return
	}
	if g.filesElsewhere {
		g.send(write, r, kittyFileElsewhereRefusal)
		return
	}
	if write == nil {
		return
	}
	g.checkFileAsync(cmd.Medium, cmd.FilePath, r, write)
}

const kittyFileElsewhereRefusal = "ENOTSUPPORTED:the path would be read on another machine"

func (g *KittyGuestImages) checkFileAsync(medium KittyGraphicsMedium, path string, r kittyUploadReply, write func([]byte)) {
	if g.checks == nil {
		g.checks = make(chan struct{}, maxKittyFileChecks)
	}
	select {
	case g.checks <- struct{}{}:
	default:
		g.send(write, r, "EBUSY:too many file uploads are being checked")
		return
	}
	checks := g.checks
	done := make(chan string, 1)
	check, timeout := kittyFileCheck, kittyFileCheckTimeout
	go func() { done <- check(medium, path) }()
	go func() {
		defer func() { <-checks }()
		select {
		case failed := <-done:
			if failed == "" {
				failed = "OK"
			}
			g.send(write, r, failed)
		case <-time.After(timeout):
			g.send(write, r, "EBADF:Failed to open file for graphics transmission: timed out")
			<-done
		}
	}()
}

func (g *KittyGuestImages) send(write func([]byte), r kittyUploadReply, msg string) {
	if write == nil || r.quiet >= 2 || (r.quiet >= 1 && msg == "OK") {
		return
	}
	var reply []byte
	reply = append(reply, "\x1b_G"...)
	sep := ""
	if r.id != 0 {
		reply = fmt.Appendf(reply, "i=%d", r.id)
		sep = ","
	}
	if r.number != 0 {
		reply = fmt.Appendf(reply, "%sI=%d", sep, r.number)
		sep = ","
	}
	if r.placement != 0 {
		reply = fmt.Appendf(reply, "%sp=%d", sep, r.placement)
	}
	reply = append(reply, ';')
	reply = append(reply, msg...)
	write(append(reply, "\x1b\\"...))
}

var pngSignature = []byte("\x89PNG\r\n\x1a\n")

func kittyPNGHeadError(cmd *KittyCommand) string {
	if cmd.Format != KittyFormatPNG || cmd.Medium != KittyMediumDirect ||
		cmd.Compression == KittyCompressionZlib || cmd.pngHead == nil {
		return ""
	}
	head := cmd.pngHead
	if len(head) < 24 {
		if cmd.More {
			return ""
		}
		return "EBADPNG:Failed to decode PNG image data"
	}
	if !bytes.Equal(head[:8], pngSignature) || string(head[12:16]) != "IHDR" ||
		binary.BigEndian.Uint32(head[16:20]) == 0 || binary.BigEndian.Uint32(head[20:24]) == 0 {
		return "EBADPNG:Failed to decode PNG image data"
	}
	return ""
}
