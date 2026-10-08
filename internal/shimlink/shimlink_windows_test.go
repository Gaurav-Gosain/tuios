//go:build windows

package shimlink

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

const sleepEnv = "TUIOS_SHIMLINK_SLEEP"

// TestHelperSleep is the program the link runs in the test below.
func TestHelperSleep(t *testing.T) {
	if os.Getenv(sleepEnv) == "" {
		t.Skip("a helper, run by TestInstallReplacesARunningLink")
	}
	time.Sleep(time.Minute)
}

// TestInstallReplacesARunningLink is the Windows boundary InstallFile is
// built around: Windows refuses to replace or delete a program that runs,
// and a plugin can be running herdr.exe when the daemon starts. The old
// file is moved aside, and a later start removes it.
func TestInstallReplacesARunningLink(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	v1 := filepath.Join(dir, "v1", "tuios.exe")
	v2 := filepath.Join(dir, "v2", "tuios.exe")
	copyExe(t, self, v1, nil)
	copyExe(t, self, v2, []byte("v2")) // trailing bytes: still a program, another file

	link, err := Install(filepath.Join(dir, "run"), "herdr.exe", v1)
	if err != nil {
		t.Fatal(err)
	}
	run := exec.Command(link, "-test.run=^TestHelperSleep$")
	run.Env = append(os.Environ(), sleepEnv+"=1")
	if err := run.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = run.Process.Kill(); _ = run.Wait() }()
	time.Sleep(500 * time.Millisecond) // the image is mapped once it runs

	if _, err := Install(filepath.Join(dir, "run"), "herdr.exe", v2); err != nil {
		t.Fatalf("Install over a running link: %v", err)
	}
	if !sameBytes(OSFS{}, link, v2) {
		t.Fatal("the link was not replaced")
	}

	_ = run.Process.Kill()
	_ = run.Wait()
	// Windows can take a moment to let go of an ended program's file.
	var left []string
	for deadline := time.Now().Add(5 * time.Second); ; {
		if _, err := Install(filepath.Join(dir, "run"), "herdr.exe", v2); err != nil {
			t.Fatal(err)
		}
		left, _ = filepath.Glob(link + ".*")
		if len(left) == 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(left) != 0 {
		t.Fatalf("left behind %v", left)
	}
}

func copyExe(t *testing.T, src, dst string, extra []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if _, err := out.Write(extra); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}
