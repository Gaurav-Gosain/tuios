package app

import (
	"os"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// newQueryTestPassthrough builds a passthrough whose host either can or cannot
// read files from this machine, without going near a real terminal.
func newQueryTestPassthrough(t *testing.T, hostReadsFiles bool) *KittyPassthrough {
	t.Helper()
	devnull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = devnull.Close() })

	// clientCapabilities takes precedence in GetHostCapabilities, so setting
	// it keeps the test off the real terminal's detection path entirely.
	previous := clientCapabilities
	clientCapabilities = &HostCapabilities{
		KittyGraphics:     true,
		KittyFileTransfer: hostReadsFiles,
		CellWidth:         9,
		CellHeight:        20,
	}
	t.Cleanup(func() { clientCapabilities = previous })

	kp := NewKittyPassthroughWithOptions(KittyPassthroughOptions{Output: devnull})
	kp.enabled = true
	// ForceEnable would also pin inlineGraphics; leave it off so the test
	// exercises the capability rather than the tuios-web override.
	kp.inlineGraphics = false
	return kp
}

func queryResponse(kp *KittyPassthrough, cmd *vt.KittyCommand) string {
	var got []byte
	kp.forwardQuery(cmd, nil, func(response []byte) { got = append(got, response...) })
	return string(got)
}

// TestForwardQueryReportsFileMediaUnsupported pins the fix for kitty graphics
// failing when tuios runs inside a browser-backed terminal such as sip's.
//
// kitten icat probes direct, temp-file and shared-memory transmission and
// commits to whichever comes back OK. Answering OK to a file medium the host
// cannot read makes it send a path that the host silently drops, and no image
// is ever drawn.
func TestForwardQueryReportsFileMediaUnsupported(t *testing.T) {
	kp := newQueryTestPassthrough(t, false)

	if got := queryResponse(kp, &vt.KittyCommand{ImageID: 1, Medium: vt.KittyMediumDirect}); got != "\x1b_Gi=1;OK\x1b\\" {
		t.Errorf("direct transmission should be supported, got %q", got)
	}

	for name, medium := range map[string]vt.KittyGraphicsMedium{
		"temp file":     vt.KittyMediumTempFile,
		"file":          vt.KittyMediumFile,
		"shared memory": vt.KittyMediumSharedMemory,
	} {
		got := queryResponse(kp, &vt.KittyCommand{ImageID: 2, Medium: medium})
		want := "\x1b_Gi=2;ENOTSUPPORTED:host terminal cannot read files from this machine\x1b\\"
		if got != want {
			t.Errorf("%s: got %q, want %q", name, got, want)
		}
	}
}

// TestForwardQueryAllowsFileMediaOnCapableHost keeps the cheap path: a host
// sharing our filesystem reads the file itself and we never copy the pixels.
func TestForwardQueryAllowsFileMediaOnCapableHost(t *testing.T) {
	kp := newQueryTestPassthrough(t, true)

	for name, medium := range map[string]vt.KittyGraphicsMedium{
		"temp file":     vt.KittyMediumTempFile,
		"shared memory": vt.KittyMediumSharedMemory,
	} {
		if got := queryResponse(kp, &vt.KittyCommand{ImageID: 7, Medium: medium}); got != "\x1b_Gi=7;OK\x1b\\" {
			t.Errorf("%s: got %q, want OK", name, got)
		}
	}
}

// TestForwardQueryQuietMode: q=1 drops successes only, q=2 drops everything.
// An error has to survive q=1 or a guest waits out its timeout rather than
// trying the next medium.
func TestForwardQueryQuietMode(t *testing.T) {
	kp := newQueryTestPassthrough(t, false)

	if got := queryResponse(kp, &vt.KittyCommand{ImageID: 1, Medium: vt.KittyMediumDirect, Quiet: 1}); got != "" {
		t.Errorf("q=1 should suppress a success, got %q", got)
	}
	if got := queryResponse(kp, &vt.KittyCommand{ImageID: 1, Medium: vt.KittyMediumTempFile, Quiet: 1}); got == "" {
		t.Error("q=1 should still report an error")
	}
	if got := queryResponse(kp, &vt.KittyCommand{ImageID: 1, Medium: vt.KittyMediumTempFile, Quiet: 2}); got != "" {
		t.Errorf("q=2 should suppress everything, got %q", got)
	}
}

func TestKittyProbeOK(t *testing.T) {
	cases := []struct {
		name     string
		response string
		id       int
		want     bool
	}{
		{"ok for the asked id", "\x1b_Gi=2;OK\x1b\\", 2, true},
		{"error for the asked id", "\x1b_Gi=2;ENOTSUPPORTED:no\x1b\\", 2, false},
		{"another id's ok does not count", "\x1b_Gi=1;OK\x1b\\", 2, false},
		{"picks its own id out of several", "\x1b_Gi=1;OK\x1b\\\x1b_Gi=2;EBADF:x\x1b\\", 2, false},
		{"unanswered", "\x1b[?62;4c", 2, false},
		{"id-less ok does not count", "\x1b_GOK\x1b\\", 2, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := kittyProbeOK(tc.response, tc.id); got != tc.want {
				t.Errorf("kittyProbeOK(%q, %d) = %v, want %v", tc.response, tc.id, got, tc.want)
			}
		})
	}
}
