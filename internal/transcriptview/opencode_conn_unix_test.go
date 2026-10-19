//go:build linux || darwin

package transcriptview

import (
	"os"
	"testing"
)

// wantsServicePID: on Linux and macOS the uid of the socket is the proof,
// and the pid in service.json is checked against /api/info afterwards.
const wantsServicePID = false

// asOtherUser makes the connection check look for another user's uid.
func asOtherUser(t *testing.T) {
	old := myUID
	myUID = func() int { return os.Getuid() + 1 }
	t.Cleanup(func() { myUID = old })
}
