package session

import (
	"os"

	"tuios.dev/vt"
)

// kittyFileMediumRefusal is the answer to a guest asking whether it may send a
// kitty image as a path (t=f, t=t or t=s) when that path would be read on a
// machine other than the one it names a file on.
const kittyFileMediumRefusal = "ENOTSUPPORTED:the path would be read on another machine"

// kittyQueryResponse is the daemon's answer to a guest's a=q graphics query,
// or nil when the guest asked not to be answered.
//
// The daemon answers queries itself, because an answer that has to go out to
// a client and back is late enough that a probing guest gives up first. That
// leaves it answering for clients it cannot ask, so it answers from what the
// clients told it when they attached, and from where a path the guest sends
// will be read.
//
// A query is not answered at all while no attached client's terminal draws
// kitty graphics (see SetKittyAdvertised): such a client drops the image, and
// silence is what a terminal without kitty graphics gives, so a guest that
// probes the way kitty documents (a query, then DA1) falls back to text or
// sixel. The rule is the one DA1 follows for sixel: any attached client that
// shows the image is enough, and with no client attached the last answer
// stands. A standalone tuios on such a terminal is silent too.
//
// Otherwise direct transmission (t=d) always works. The bytes are in the
// stream, and every client that draws the pane receives them.
//
// A file medium names something on the machine the guest runs on, and the
// client that draws the pane reads it there, or hands the path to its host
// terminal, which reads it on the client's machine. That is the same machine
// only when both the pane and every client drawing it are on this one. So a
// file medium is refused, and the guest falls back to direct transmission,
// when either is not:
//
//   - remotePane: the pane's process runs on another machine over a link, so
//     the path it sends names a file over there. A far daemon that reads file
//     media itself (kitty_link_far.go) answers or rewrites such a query
//     before it reaches this one, so only a far daemon from before that sends
//     one here.
//   - a client attached over a link (see Session.SetLinkedViewer) is drawing
//     the session from another machine, where the path names nothing, or
//     names some other file.
//
// A shared memory object (t=s) is refused as well on a machine where it has no
// path, which is macOS: there it is opened by name with shm_open, and Go
// reaches that only through cgo. tuios can then only hand the name to the
// host terminal, and a client whose host does not read files on this machine
// drops the frame. The daemon cannot tell which clients those are, so it never
// offers the medium there. kitten icat then uses a temporary file, which every
// client can read or forward.
//
// kitten icat is the guest this is written for. It probes direct, temporary
// file and shared memory transmission at once and uses the best medium that
// comes back OK, so an honest refusal costs it nothing but a copy through the
// stream, while an OK that cannot be honoured draws nothing at all.
//
// Quiet is honoured as kitty honours it: q=1 suppresses an OK and still sends
// an error, q=2 suppresses both.
func (s *Session) kittyQueryResponse(cmd *vt.KittyCommand, remotePane bool) []byte {
	if !s.kittyAdvertised.Load() {
		return nil
	}
	ok := true
	msg := ""
	switch {
	case cmd.Medium.IsFile() && (remotePane || s.linkedViewer.Load()):
		ok = false
		msg = kittyFileMediumRefusal
	case cmd.Medium == vt.KittyMediumSharedMemory && !kittySharedMemoryHasPath():
		ok = false
		msg = kittySharedMemoryRefusal
	}
	if cmd.Quiet >= 2 || (cmd.Quiet >= 1 && ok) {
		return nil
	}
	return vt.BuildKittyResponse(ok, cmd.ImageID, msg)
}

// kittySharedMemoryRefusal is the answer to a t=s query on a machine where a
// shared memory object has no path. See kittyQueryResponse.
const kittySharedMemoryRefusal = "ENOTSUPPORTED:shared memory is not supported here"

// kittySharedMemoryHasPath reports whether a shared memory object on this
// machine is a file under /dev/shm, as on Linux. It is a variable so a test can
// pin the answer on any platform.
var kittySharedMemoryHasPath = func() bool {
	info, err := os.Stat("/dev/shm")
	return err == nil && info.IsDir()
}

// SetLinkedViewer records whether a client attached over a link, from another
// machine, is drawing this session. The daemon sets it whenever the set of
// attached clients changes. See kittyQueryResponse for what reads it.
func (s *Session) SetLinkedViewer(linked bool) {
	s.linkedViewer.Store(linked)
}

// kittyAnimationRefusal is the daemon's answer to a frame edit (a=f, a=a,
// a=c) when the attached client's host cannot make it, or nil. It is written
// from the pane's emulator, so it reaches the guest in order with the answer
// to everything the guest asked before and after it. See
// Session.SetKittyAnimation. A pane on another machine is answered there.
func kittyAnimationRefusal(cmd *vt.KittyCommand, remotePane, hostAnimates bool) []byte {
	if remotePane || hostAnimates || !vt.IsKittyAnimationAction(cmd.Action) || cmd.Quiet >= 2 {
		return nil
	}
	return vt.BuildKittyResponse(false, cmd.ImageID, vt.KittyAnimationRefusal)
}
