package session

import (
	"os"

	"github.com/Gaurav-Gosain/tuios/internal/kittyshm"
	"tuios.dev/vt"
)

const kittyFileMediumRefusal = "ENOTSUPPORTED:the path would be read on another machine"

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
	case cmd.Medium == vt.KittyMediumSharedMemory && !kittySharedMemoryReachable():
		ok = false
		msg = kittySharedMemoryRefusal
	}
	if cmd.Quiet >= 2 || (cmd.Quiet >= 1 && ok) {
		return nil
	}
	return vt.BuildKittyResponse(ok, cmd.ImageID, msg)
}

const kittySharedMemoryRefusal = "ENOTSUPPORTED:shared memory is not supported here"

var kittySharedMemoryReachable = func() bool {
	if kittyshm.Available() {
		return true
	}
	info, err := os.Stat("/dev/shm")
	return err == nil && info.IsDir()
}

// SetLinkedViewer records whether a client attached over a link, from another
// machine, is drawing this session. The daemon sets it whenever the set of
// attached clients changes. See kittyQueryResponse for what reads it.
func (s *Session) SetLinkedViewer(linked bool) {
	s.linkedViewer.Store(linked)
}

func kittyAnimationRefusal(cmd *vt.KittyCommand, remotePane, hostAnimates bool) []byte {
	if remotePane || hostAnimates || !vt.IsKittyAnimationAction(cmd.Action) || cmd.Quiet >= 2 {
		return nil
	}
	return vt.BuildKittyResponse(false, cmd.ImageID, vt.KittyAnimationRefusal)
}
