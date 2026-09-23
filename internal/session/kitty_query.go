package session

import "github.com/Gaurav-Gosain/tuios/internal/vt"

// kittyFileMediumRefusal is the answer to a guest asking whether it may send a
// kitty image as a path (t=f, t=t or t=s) when that path would be read on a
// machine other than the one it names a file on.
const kittyFileMediumRefusal = "ENOTSUPPORTED:the path would be read on another machine"

// kittyQueryResponse is the daemon's answer to a guest's a=q graphics query,
// or nil when the guest asked not to be answered.
//
// The daemon answers queries itself, because an answer that has to go out to
// a client and back is late enough that a probing guest gives up first. That
// leaves it answering for clients it cannot ask, so it answers for the one
// thing it knows: where a path the guest sends will be read.
//
// Direct transmission (t=d) always works. The bytes are in the stream, and
// every client that draws the pane receives them.
//
// A file medium names something on the machine the guest runs on, and the
// client that draws the pane reads it there, or hands the path to its host
// terminal, which reads it on the client's machine. That is the same machine
// only when both the pane and every client drawing it are on this one. So a
// file medium is refused, and the guest falls back to direct transmission,
// when either is not:
//
//   - remotePane: the pane's process runs on another machine over a link, so
//     the path it sends names a file over there.
//   - a client attached over a link (see Session.SetLinkedViewer) is drawing
//     the session from another machine, where the path names nothing, or
//     names some other file.
//
// kitten icat is the guest this is written for. It probes direct, temporary
// file and shared memory transmission at once and uses the best medium that
// comes back OK, so an honest refusal costs it nothing but a copy through the
// stream, while an OK that cannot be honoured draws nothing at all.
//
// Quiet is honoured as kitty honours it: q=1 suppresses an OK and still sends
// an error, q=2 suppresses both.
func (s *Session) kittyQueryResponse(cmd *vt.KittyCommand, remotePane bool) []byte {
	ok := true
	msg := ""
	if cmd.Medium.IsFile() && (remotePane || s.linkedViewer.Load()) {
		ok = false
		msg = kittyFileMediumRefusal
	}
	if cmd.Quiet >= 2 || (cmd.Quiet >= 1 && ok) {
		return nil
	}
	return vt.BuildKittyResponse(ok, cmd.ImageID, msg)
}

// SetLinkedViewer records whether a client attached over a link, from another
// machine, is drawing this session. The daemon sets it whenever the set of
// attached clients changes. See kittyQueryResponse for what reads it.
func (s *Session) SetLinkedViewer(linked bool) {
	s.linkedViewer.Store(linked)
}
