package session

import (
	"fmt"

	"github.com/Gaurav-Gosain/tuios/internal/errfmt"
)

// ProtocolVersion is the wire protocol this build speaks. Both sides announce it
// in the handshake (HelloPayload.Protocol, WelcomePayload.Protocol) and both
// refuse a peer outside the range they serve.
//
// Bump it on any change an older peer cannot read. Appending a message type to
// the end of the iota block is not such a change; inserting one is, because the
// type is a single byte on the wire and every value after the insertion moves.
//
// 3 is this version because exactly that happened since v0.7.0: MsgCapturePane
// was inserted after MsgSetConfig (f0810a1), which moved MsgWelcome from 22 to
// 23 and every server-to-client type with it, while ProtocolVersion stayed at 2.
// A v0.7.0 daemon answers a hello with type 22, which this build reads as
// MsgCapturePane. It was found by building the v0.7.0 tag and pointing this
// client at it. The number is corrected here rather than the numbering reverted,
// because no released build speaks 3 yet, so the bump costs nothing and the
// refusal it enables is the honest outcome for a pairing that cannot work.
const ProtocolVersion = 3

// MinProtocolVersion is the oldest wire protocol this build still serves. A peer
// announcing anything older is told to upgrade rather than allowed to proceed
// into undefined behavior.
const MinProtocolVersion = 3

// LegacyProtocolVersion is what a peer that announces nothing is taken to speak.
// gob leaves a field the sender did not know at its zero value, so silence is
// age: a build from before the version fields existed, which is a build from
// before the numbering moved. It is outside the range above, so such a peer is
// refused, which is correct: it cannot read this build's messages.
const LegacyProtocolVersion = 2

func peerProtocol(announced int) int {
	if announced == 0 {
		return LegacyProtocolVersion
	}
	return announced
}

func protocolMismatch(announced int) bool {
	v := peerProtocol(announced)
	return v > ProtocolVersion || v < MinProtocolVersion
}

func numberingMismatch(clientVersion string, got MessageType) *ProtocolMismatchError {
	return &ProtocolMismatchError{
		ClientVersion:  clientVersion,
		DaemonPID:      GetDaemonPID(),
		ClientProtocol: ProtocolVersion,
		Cause:          errfmt.Errorf("the daemon answered the handshake with message type %d, which this build does not read as a welcome", got),
	}
}

func clientProtocolRefusal(daemonVersion string, hello *HelloPayload) string {
	client := peerProtocol(hello.Protocol)
	if client > ProtocolVersion {
		return fmt.Sprintf("This daemon (version %s) speaks wire protocol %d and the client (version %s) speaks %d. "+
			"TUIOS was upgraded while the daemon kept running. "+
			"Fix: run 'tuios kill-server', then start tuios again; sessions are saved and restored across the restart.",
			daemonVersion, ProtocolVersion, hello.Version, client)
	}
	return fmt.Sprintf("This daemon (version %s) no longer serves wire protocol %d, and the client (version %s) speaks it. "+
		"Fix: upgrade tuios, or run 'tuios kill-server' and start again with the version you want.",
		daemonVersion, client, hello.Version)
}

func daemonProtocolMismatch(clientVersion string, welcome *WelcomePayload) *ProtocolMismatchError {
	return &ProtocolMismatchError{
		ClientVersion:  clientVersion,
		DaemonVersion:  welcome.Version,
		DaemonPID:      GetDaemonPID(),
		Sessions:       len(welcome.SessionNames),
		ClientProtocol: ProtocolVersion,
		DaemonProtocol: peerProtocol(welcome.Protocol),
	}
}
