package session

import "errors"

// errPaneReconnecting is what a write gets while the link is being restored.
var errPaneReconnecting = errors.New("the link to this pane's machine is down and tuios is reconnecting")

// PublishLiveFacts pushes the session's state to its clients because something
// the emulators or the processes know has changed, rather than because the
// session itself was altered.
//
// A remote pane's directory is the case it exists for. Nothing about the
// session changes when a shell on another machine runs cd: the window set, the
// layout and the names are all as they were, so no mutation happens and no
// push follows. But the directory is on the snapshot clients are given, and
// without a push they would be told the first answer and never a later one.
//
// A pane on this machine has no such problem, which is why this was not needed
// before: its shell announces over OSC 7, and that announcement reaches every
// client through its own emulator rather than through the session's state.
//
// It goes through mutateState with an empty change so it takes the version
// with it. The push is skipped for a version already sent, so a bump is what
// makes it a push at all, and a version is the honest record anyway: what
// clients hold afterwards is not what they held before.
func (s *Session) PublishLiveFacts() {
	// The cached read goes first. liveCwds holds its answer for a second so
	// the render path does not ask the operating system per frame, and the
	// push that created the pane had already cached the answer from before the
	// far machine replied: an empty one. Publishing without clearing it sent
	// that same empty answer again, which is the whole reason this was still
	// broken after the push itself was fixed.
	s.forgetCwdCache()
	_ = s.mutateState(func(*SessionState) error { return nil })
}
