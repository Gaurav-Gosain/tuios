package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestDetectClipboardToolMatchesEnv verifies that detection is consistent with
// the environment: when a Wayland session + wl-clipboard are present we must
// find a tool; when neither display nor tool exists we must return nil. This
// keeps the guard logic honest without requiring a compositor in CI.
func TestDetectClipboardToolMatchesEnv(t *testing.T) {
	wayland := os.Getenv("WAYLAND_DISPLAY") != "" && os.Getenv("XDG_RUNTIME_DIR") != ""
	x11 := os.Getenv("DISPLAY") != ""
	hasWLCopy := lookPath("wl-copy") && lookPath("wl-paste")
	hasXClip := lookPath("xclip") || lookPath("xsel")
	hasPB := lookPath("pbcopy") && lookPath("pbpaste")

	tool := DetectClipboardTool()

	expectTool := (wayland && hasWLCopy) || (x11 && hasXClip) || hasPB
	if expectTool && tool == nil {
		t.Fatalf("environment has a usable clipboard (wayland=%v x11=%v wl=%v xclip=%v pb=%v) but DetectClipboardTool returned nil",
			wayland, x11, hasWLCopy, hasXClip, hasPB)
	}
	if !expectTool && tool != nil {
		t.Fatalf("DetectClipboardTool returned %q but no clipboard tool is detectable in this environment", tool.name)
	}
	if tool != nil && (tool.copyCmd == nil || tool.pasteCmd == nil) {
		t.Fatalf("tool %q has incomplete commands: copy=%v paste=%v", tool.name, tool.copyCmd, tool.pasteCmd)
	}
}

// TestClipboardToolWriteReadLogic exercises Write/Read against a fake tool
// built from shell commands that need no compositor (tee/cat to a temp file).
// This is the deterministic part of the contract: Write launches the keeper
// without blocking, Read captures stdout. The real wl-copy round trip is
// covered by TestClipboardToolRoundTrip, which runs only when a Wayland socket
// is actually reachable (never in the isolated test tree).
func TestClipboardToolWriteReadLogic(t *testing.T) {
	clipFile := filepath.Join(t.TempDir(), "clip.txt")

	fake := &clipboardTool{
		name:     "fake-file",
		copyCmd:  []string{"sh", "-c", "cat > " + clipFile},
		pasteCmd: []string{"cat", clipFile},
	}

	// Write must return without blocking (Start semantics) and the keeper
	// process must have been launched.
	if err := fake.Write("hello-tuios-clipboard"); err != nil {
		t.Fatalf("Write failed: %v", err)
	}

	// The fake keeper writes asynchronously (Start semantics, like wl-copy's
	// real keeper), so poll briefly for the content instead of sleeping: a
	// real wl-copy keeper stays alive until replaced, which is fine — we only
	// care that the content reached the clipboard.
	var got string
	for i := 0; i < 50; i++ {
		var err error
		got, err = fake.Read()
		if err == nil && strings.Contains(got, "hello-tuios-clipboard") {
			break
		}
		// tiny sleep to let the keeper land the bytes
		time.Sleep(10 * time.Millisecond)
		if i == 49 {
			t.Fatalf("round trip mismatch: wrote %q, read back %q (err=%v)", "hello-tuios-clipboard", got, err)
		}
	}

	// Write again with new content: the second keeper must replace the first
	// (for wl-clipboard this is a natural handoff; for the fake it is a new
	// write to the same file).
	if err := fake.Write("second-write"); err != nil {
		t.Fatalf("second Write failed: %v", err)
	}
	var got2 string
	for i := 0; i < 50; i++ {
		var err error
		got2, err = fake.Read()
		if err == nil && strings.Contains(got2, "second-write") {
			break
		}
		time.Sleep(10 * time.Millisecond)
		if i == 49 {
			t.Fatalf("second round trip mismatch: read back %q (err=%v)", got2, err)
		}
	}
}

// TestClipboardToolRoundTrip writes a sentinel through the real native tool
// and reads it back. It only runs when a Wayland socket is actually reachable
// from the test process: the package TestMain redirects XDG_RUNTIME_DIR to a
// throwaway tree, so under `go test` this always skips — which is exactly what
// we want, because the isolated tree has no compositor socket. Run it manually
// (or in a non-isolated harness) to validate against a live session.
func TestClipboardToolRoundTrip(t *testing.T) {
	tool := DetectClipboardTool()
	if tool == nil {
		t.Skip("no native clipboard tool detected in this environment")
	}

	// The package TestMain points XDG_RUNTIME_DIR at a temp tree. Without a
	// real wayland socket there, wl-copy/wl-paste cannot reach any compositor,
	// so the round trip is meaningless here. Detect that and skip.
	rt := os.Getenv("XDG_RUNTIME_DIR")
	if rt == "" || !strings.Contains(rt, "run/user") {
		t.Skipf("XDG_RUNTIME_DIR=%q is not a real user runtime dir (test tree?), skipping live round trip", rt)
	}
	if _, err := os.Stat(filepath.Join(rt, os.Getenv("WAYLAND_DISPLAY"))); err != nil {
		t.Skipf("no Wayland socket at %s/%s: %v", rt, os.Getenv("WAYLAND_DISPLAY"), err)
	}

	sentinel := "tuios-clipboard-test-" + strings.Repeat("x", 8)
	if err := tool.Write(sentinel); err != nil {
		t.Fatalf("Write failed: %v", err)
	}
	got, err := tool.Read()
	if err != nil {
		t.Fatalf("Read failed: %v (tool=%s, XDG_RUNTIME_DIR=%q, WAYLAND_DISPLAY=%q)",
			err, tool.name, rt, os.Getenv("WAYLAND_DISPLAY"))
	}
	if !strings.Contains(got, "tuios-clipboard-test") {
		t.Fatalf("round trip mismatch: wrote %q, read back %q", sentinel, got)
	}
}

// TestLocalClipboardAvailableMatchesDetect checks that the boolean helper agrees
// with DetectClipboardTool, so callers cannot see a tool through one and not
// the other.
func TestLocalClipboardAvailableMatchesDetect(t *testing.T) {
	if LocalClipboardAvailable() != (DetectClipboardTool() != nil) {
		t.Fatal("LocalClipboardAvailable() and DetectClipboardTool() disagree")
	}
}
