package app

import (
	"bytes"
	"strings"
	"testing"
)

// withClientCaps installs client capabilities for the duration of a test and
// restores whatever was there before. GetHostCapabilities prefers these.
func withClientCaps(t *testing.T, caps *HostCapabilities) {
	t.Helper()
	prev := clientCapabilities
	clientCapabilities = caps
	t.Cleanup(func() { clientCapabilities = prev })
}

// TestKittyPassthrough_EnabledForKittyClient mirrors the SSH daemon/ephemeral
// wiring: the client's detected capabilities are installed as the host caps and
// graphics output is routed to a session writer. A kitty-capable client must
// enable the passthrough and have its APC bytes land on that writer, not the
// server's stdout.
func TestKittyPassthrough_EnabledForKittyClient(t *testing.T) {
	withClientCaps(t, &HostCapabilities{KittyGraphics: true, TerminalName: "kitty"})

	var out bytes.Buffer
	kp := NewKittyPassthroughWithOptions(KittyPassthroughOptions{
		Output:       &out,
		RemoteClient: true,
	})
	if !kp.IsEnabled() {
		t.Fatal("expected kitty passthrough enabled for a kitty client")
	}

	kp.WriteToHost([]byte("PAYLOAD"))
	if !strings.Contains(out.String(), "PAYLOAD") {
		t.Fatalf("expected graphics written to the routed output, got %q", out.String())
	}
}

// TestKittyPassthrough_DisabledForPlainClient is the negative case: a client
// with no kitty support must leave the passthrough disabled so no APC bytes are
// ever forwarded to a terminal that cannot render them.
func TestKittyPassthrough_DisabledForPlainClient(t *testing.T) {
	withClientCaps(t, &HostCapabilities{KittyGraphics: false, TerminalName: "xterm"})

	var out bytes.Buffer
	kp := NewKittyPassthroughWithOptions(KittyPassthroughOptions{
		Output:       &out,
		RemoteClient: true,
	})
	if kp.IsEnabled() {
		t.Fatal("expected kitty passthrough disabled for a plain client")
	}
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

// TestSixelPassthrough_EnabledForSixelClient checks the sixel side of the same
// routing: a sixel-capable client enables the passthrough against the routed
// writer.
func TestSixelPassthrough_EnabledForSixelClient(t *testing.T) {
	withClientCaps(t, &HostCapabilities{SixelGraphics: true, TerminalName: "foot"})

	var out bytes.Buffer
	sp := NewSixelPassthroughWithOptions(SixelPassthroughOptions{Output: &out})
	if !sp.IsEnabled() {
		t.Fatal("expected sixel passthrough enabled for a sixel client")
	}
}
