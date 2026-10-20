package transcriptview

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// TestOpenCodeServiceDACL: on Windows the DACL of service.json stands in for
// the Unix mode&022 check. A file only its owner can change is read. The same
// file is refused once Everyone may write it, and the password never goes
// out: whoever can write service.json chooses where it goes.
func TestOpenCodeServiceDACL(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("testdata", "opencode-messages.json"))
	if err != nil {
		t.Fatal(err)
	}
	me, _, err := currentUser()
	if err != nil {
		t.Fatal(err)
	}
	noOpenCode(t)
	f := newFakeOpenCode(t, page)
	path := writeService(t, f.srv.URL, f.pid, 0o600)
	icacls := func(args ...string) {
		t.Helper()
		out, err := exec.Command("icacls", append([]string{path}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("icacls %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	icacls("/inheritance:r", "/grant:r", "*"+me.String()+":F")
	p, err := Read(path, Options{Format: FormatOpenCode, Session: ocSession})
	if err != nil {
		t.Fatalf("a service.json only its owner can change: %v", err)
	}
	checkOpenCodePage(t, p)

	// S-1-1-0 is Everyone. The read above is shared with later reads of the
	// same file for a moment, so it is dropped first.
	icacls("/grant", "*S-1-1-0:(W)")
	openCodeFlights.Lock()
	openCodeFlights.m = nil
	openCodeFlights.Unlock()
	before := f.hits.Load()
	if _, err := Read(path, Options{Format: FormatOpenCode, Session: ocSession}); err != ErrService {
		t.Fatalf("a service.json Everyone can write: err %v, want ErrService", err)
	}
	if n := f.hits.Load() - before; n != 0 {
		t.Fatalf("%d requests carried the password after Everyone could write service.json", n)
	}
}
