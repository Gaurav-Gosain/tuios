//go:build windows

package transcriptview

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// TestOpenCodeExportTree: when the context of the export ends, the private server
// opencode started under it ends too. The fake opencode starts a child and
// hangs. Without the job object only the fake is killed, and the child keeps
// running for a minute.
func TestOpenCodeExportTree(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	copyFile(t, exe, filepath.Join(bin, "opencode.exe"))
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	t.Setenv("PATH", bin)
	t.Setenv(fakeOpenCodeEnv, "tree")
	t.Setenv(fakeOpenCodePIDEnv, pidFile)

	// The context ends once the child runs, as a timeout would end it.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := openCodeFromExport(ctx, ocSession)
		done <- err
	}()
	var data []byte
	for deadline := time.Now().Add(20 * time.Second); len(data) == 0; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the fake opencode did not start its child")
		}
		data, _ = os.ReadFile(pidFile)
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("an export that was cancelled gave no error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the export did not return after its context ended")
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		return // The child is gone.
	}
	defer func() { _ = windows.CloseHandle(h) }()
	if ev, _ := windows.WaitForSingleObject(h, 5000); ev != windows.WAIT_OBJECT_0 {
		_ = windows.TerminateProcess(h, 1)
		t.Fatal("the child of the export outlived the export's timeout")
	}
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	src, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = src.Close() }()
	dst, err := os.Create(to)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		t.Fatal(err)
	}
	if err := dst.Close(); err != nil {
		t.Fatal(err)
	}
}
