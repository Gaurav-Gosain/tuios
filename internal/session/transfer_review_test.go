package session

import (
	"errors"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/config"
)

// Security boundaries the review of the transfer engine found. These are kept
// unit tests because each is a deterministic policy decision the E2E suite
// cannot reach without a second machine linking in.

// TestTransferEventsReachOnlyAFilesLink: a copy is the person's, so its events
// reach a link only when the link may list the transfers, which needs files
// and the relay capabilities, not list alone. A list-only link that
// subscribes must not read a copy's paths from the stream.
func TestTransferEventsReachOnlyAFilesLink(t *testing.T) {
	d, _ := startTestDaemon(t)
	d.SetLinkPolicies(map[string]config.HostConfig{
		"viewer": {Allow: []string{"list"}},
		"mover":  {Allow: []string{"list", "mail", "open", "write", "respond", "files"}},
	})
	ev := streamEvent{Type: EventTransfer, Action: "created", Transfer: &TransferRow{ID: "x", Name: "payroll.xlsx"}}

	viewer := &connState{viaLink: true, linkPeer: "viewer", linkPeerSet: true}
	if d.eventInScope(viewer, ev) {
		t.Errorf("a list-only link received a transfer event")
	}
	mover := &connState{viaLink: true, linkPeer: "mover", linkPeerSet: true}
	if !d.eventInScope(mover, ev) {
		t.Errorf("a link with files and relay did not receive a transfer event")
	}
	local := &connState{}
	if !d.eventInScope(local, ev) {
		t.Errorf("the person's own connection did not receive a transfer event")
	}
}

// TestAnOldFiveCapAllowStillRelays: a host table written before files existed
// that allows all five capabilities keeps relaying through this machine. The
// transfer verbs need files on top of the relay set, so the same table cannot
// start a transfer until files is added.
func TestAnOldFiveCapAllowStillRelays(t *testing.T) {
	d, _ := startTestDaemon(t)
	d.SetLinkPolicies(map[string]config.HostConfig{
		"laptop": {Allow: []string{"list", "mail", "open", "write", "respond"}},
		"phone":  {Allow: []string{"list", "mail", "open", "write", "respond", "files"}},
	})
	five := &connState{viaLink: true, linkPeer: "laptop", linkPeerSet: true}
	if verr := d.checkLinkVerb(five, "open-host-connection"); verr != nil {
		t.Errorf("a five-capability table was refused relaying: %v", verr.Message)
	}
	if verr := d.checkLinkVerb(five, "transfer-start"); verr == nil || verr.Code != ErrVerbForbidden {
		t.Errorf("transfer-start without files was not refused: %v", verr)
	}
	withFiles := &connState{viaLink: true, linkPeer: "phone", linkPeerSet: true}
	if verr := d.checkLinkVerb(withFiles, "transfer-start"); verr != nil {
		t.Errorf("transfer-start with files and the relay set was refused: %v", verr.Message)
	}
}

// TestAFarWriteErrorCodeIsNotRetried: a write the far side could not make,
// such as a full disk, comes back on the line after the bytes with its code,
// and the job ends. Without the code it would read as an unreachable host and
// the job would retry for ever. A reply with no code, from a far daemon that
// predates codes or a sender that stopped, is still retried.
func TestAFarWriteErrorCodeIsNotRetried(t *testing.T) {
	line := []byte(`{"part_size":4096,"written":4096,"error":"write: the disk is full","code":"disk_full"}`)
	var te *transferError
	if !errors.As(classify(writeReplyError(line, "build", 8192)), &te) {
		t.Fatalf("classify did not give a transferError")
	}
	if te.retry || te.code != ErrVerbDiskFull {
		t.Errorf("a far disk_full gave code %q retry %v, want disk_full and no retry", te.code, te.retry)
	}
	old := []byte(`{"part_size":4096,"written":4096,"error":"the sender stopped after 4096 of 8192 bytes"}`)
	if !errors.As(classify(writeReplyError(old, "build", 8192)), &te) || !te.retry {
		t.Errorf("a reply with no code is not retried: %+v", te)
	}
	if err := writeReplyError([]byte(`{"part_size":8192,"written":8192}`), "build", 8192); err != nil {
		t.Errorf("a whole write was refused: %v", err)
	}
}

// TestSafeRelRefusesAWindowsEscape: a name a far walk sends must not climb out
// of the destination folder on any destination. On Windows a backslash is a
// separator and a colon names a drive or a stream, so both are refused on
// every machine, since the machine that checks is not always the one that
// writes.
func TestSafeRelRefusesAWindowsEscape(t *testing.T) {
	for _, rel := range []string{
		`..\..\AppData\Roaming\Microsoft\Windows\Start Menu\Programs\Startup\evil.bat`,
		`sub\..\..\evil`,
		`C:\Windows\evil`,
		`C:evil`,
		`file.txt:stream`,
		"../evil", "/etc/passwd", "a/../../b", "", "a//b",
	} {
		if safeRel(rel) {
			t.Errorf("safeRel accepts %q", rel)
		}
	}
	for _, rel := range []string{"a", "a/b/c.txt", "photos/2026 trip/img 1.jpg", ".hidden/x"} {
		if !safeRel(rel) {
			t.Errorf("safeRel refuses the ordinary name %q", rel)
		}
	}
}
