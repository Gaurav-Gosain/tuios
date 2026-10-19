package transcriptview

import (
	"testing"

	"golang.org/x/sys/windows"
)

// wantsServicePID: on Windows the owner of the socket is a pid, which must
// be the pid in service.json.
const wantsServicePID = true

// asOtherUser makes the checks look for LocalSystem, which the test does not
// run as.
func asOtherUser(t *testing.T) {
	sys, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatal(err)
	}
	old := currentUser
	currentUser = func() (*windows.SID, *windows.SID, error) { return sys, sys, nil }
	t.Cleanup(func() { currentUser = old })
}
