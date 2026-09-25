package app

import (
	"bytes"
	"testing"
)

// withClientCaps installs client capabilities for the duration of a test and
// restores whatever was there before. GetHostCapabilities prefers these.
func withClientCaps(t *testing.T, caps *HostCapabilities) {
	t.Helper()
	prev := clientCapabilities.Load()
	clientCapabilities.Store(caps)
	t.Cleanup(func() { clientCapabilities.Store(prev) })
}

// TestKittyPassthrough_RemoteClientNeverReadsFiles verifies that a remote
// (SSH) client never resolves server-local file transmissions, even when the
// host capabilities claim file-transfer support. Over SSH the file lives on the
// server; the client can only render bytes we send it.
func TestKittyPassthrough_RemoteClientNeverReadsFiles(t *testing.T) {
	withClientCaps(t, &HostCapabilities{KittyGraphics: true, KittyFileTransfer: true, TerminalName: "kitty"})

	remote := NewKittyPassthroughWithOptions(KittyPassthroughOptions{
		Output:       &bytes.Buffer{},
		RemoteClient: true,
	})
	if remote.hostReadsFiles() {
		t.Error("expected a remote client to re-encode file transmissions as direct data")
	}

	// A local passthrough with the same host caps should still honor file
	// transfer, proving the difference is the RemoteClient flag, not the caps.
	local := NewKittyPassthroughWithOptions(KittyPassthroughOptions{
		Output: &bytes.Buffer{},
	})
	if !local.hostReadsFiles() {
		t.Error("expected a local file-transfer-capable host to read files")
	}
}
