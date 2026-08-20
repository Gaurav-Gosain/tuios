package session

import "fmt"

// This is the binary protocol's half of what verb_compat.go does for the JSON
// verb protocol: both sides announce a version in the handshake, and a peer
// outside the range this build serves is refused with the command that fixes it
// rather than allowed through to fail later as a decode or a hang.
//
// A tagged release is exactly when it bites, because that is when a daemon
// started by the previous version is still holding the socket.

// peerProtocol is the version a peer announced, with silence read as the
// version every build spoke before the field existed.
func peerProtocol(announced int) int {
	if announced == 0 {
		return LegacyProtocolVersion
	}
	return announced
}

// protocolMismatch reports whether a peer's announced version is outside the
// range this build serves.
func protocolMismatch(announced int) bool {
	v := peerProtocol(announced)
	return v > ProtocolVersion || v < MinProtocolVersion
}

// numberingMismatch describes a daemon whose reply to the hello was not a
// welcome at all. The message type is one byte on the wire with no name on it,
// so a daemon from before a type was inserted answers with a number this build
// reads as a different message. Nothing useful can be exchanged with it, and the
// version it would have announced is unreachable, so the report says what is
// known and names the fix.
func numberingMismatch(clientVersion string, got MessageType) *ProtocolMismatchError {
	return &ProtocolMismatchError{
		ClientVersion:  clientVersion,
		DaemonPID:      GetDaemonPID(),
		ClientProtocol: ProtocolVersion,
		Cause:          fmt.Errorf("the daemon answered the handshake with message type %d, which this build does not read as a welcome", got),
	}
}

// clientProtocolRefusal is what the daemon tells a client it will not serve. It
// is the message the user reads, so it says which side to move and the command
// that moves it.
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

// daemonProtocolMismatch describes a daemon this client cannot talk to, in the
// shape the CLI already reports a verb-protocol mismatch in.
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
