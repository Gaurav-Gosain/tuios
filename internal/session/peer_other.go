//go:build !linux && !darwin

package session

import "net"

// Platforms without a peer pid on a unix socket that this build reads: Windows
// (AF_UNIX has SIO_AF_UNIX_GETPEERPID, not wired here) and the BSDs (whose
// getpeereid gives the uid, not the pid). peerPID reports 0 there, and
// human_origin.go treats an unknown peer as it treated every caller before the
// check existed: the attach nonce is the only proof asked for. See
// docs/AGENT_STATE.md, "Who can act as the person".

// peerPIDSupported is false here: the daemon cannot place a caller in its
// pane by pid, so the tuios CLI presents the pane's token instead. See
// VerbClient.presentPaneToken.
const peerPIDSupported = false

func peerPID(net.Conn) int { return 0 }

func readProcLineage(int) (int, int64, bool) { return 0, 0, false }

func readProcEnvVar(int, string) (string, bool) { return "", false }
