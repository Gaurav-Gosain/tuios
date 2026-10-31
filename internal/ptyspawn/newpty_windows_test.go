//go:build windows

package ptyspawn

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/xpty"
)

// These tests pin the choice between conpty.dll and the inbox ConPTY: only
// the folder of tuios.exe may supply the DLL (a planted one would run code in
// the daemon), and a missing, unloadable or OpenConsole-less DLL leaves the
// pane on the inbox ConPTY. Each case is told apart by the pty that comes
// back and by the log, which is silent when there is no DLL to try. The E2E
// suite does not run on Windows, so the windows job in go-test.yml runs these
// as unit tests, with the real DLL from the Microsoft.Windows.Console.ConPTY
// package for the two that need it.
//
// Negative control: cutting the lookup, the fallback, the OpenConsole.exe
// check or Start's path helpers each fails the test named for it.

// garbageDLL writes a conpty.dll that LoadLibraryEx refuses, and an
// OpenConsole.exe beside it so the load is attempted.
func garbageDLL(t *testing.T, dir string) {
	t.Helper()
	for _, name := range []string{conptyDLLName, openConsoleName} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("not a PE image"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// useConPTYDLL points the lookup at dir for the duration of the test.
func useConPTYDLL(t *testing.T, dir string) {
	t.Helper()
	prev := sideBySideConPTY
	sideBySideConPTY = func() (*conptyDLL, error) { return loadConPTYDLL(dir) }
	t.Cleanup(func() { sideBySideConPTY = prev })
}

// spawnEcho opens a pane that prints marker and returns the pty and the log
// lines Spawn wrote, once marker has shown up.
func spawnEcho(t *testing.T, marker string) (xpty.Pty, []string) {
	t.Helper()
	pty, logged, _ := spawnUntil(t, marker, command(os.Getenv("ComSpec"), "/c", "echo "+marker), printed(marker))
	return pty, logged
}

// command is a newCmd for Spawn that runs argv.
func command(argv ...string) func() *exec.Cmd {
	return func() *exec.Cmd { return exec.Command(argv[0], argv[1:]...) }
}

// printed is a done for spawnUntil that waits for marker.
func printed(marker string) func([]byte) bool {
	return func(out []byte) bool { return bytes.Contains(out, []byte(marker)) }
}

// spawnUntil opens a pane running newCmd's command and reads it until done
// says the output so far holds what the test waits for, named by what. It
// returns the pty, the log lines Spawn wrote and the output. It answers the
// DA1 query OpenConsole sends when it starts, as tuios's emulator would.
func spawnUntil(t *testing.T, what string, newCmd func() *exec.Cmd, done func([]byte) bool) (xpty.Pty, []string, []byte) {
	t.Helper()
	var logged []string
	logf := func(format string, args ...any) { logged = append(logged, fmt.Sprintf(format, args...)) }
	pty, cmd, err := Spawn(80, 24, newCmd, logf)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = pty.Close()
	})

	got := make(chan []byte, 1)
	go func() {
		var out []byte
		buf := make([]byte, 4096)
		for {
			n, err := pty.Read(buf)
			out = append(out, buf[:n]...)
			if bytes.Contains(buf[:n], []byte("\x1b[c")) {
				_, _ = pty.Write([]byte("\x1b[?62;4;22c"))
			}
			if done(out) || err != nil {
				got <- out
				return
			}
		}
	}()
	select {
	case out := <-got:
		if !done(out) {
			t.Fatalf("the pane closed before it printed %s; it printed %q", what, out)
		}
		return pty, logged, out
	case <-time.After(15 * time.Second):
		t.Fatalf("the pane never printed %s", what)
	}
	return nil, nil, nil
}

// hasSixel reports whether out holds the start of a sixel image: a DCS whose
// final byte is q, as in ESC P q or ESC P 0;0;0 q.
func hasSixel(out []byte) bool {
	for i := bytes.Index(out, []byte("\x1bP")); i >= 0; {
		params := bytes.TrimLeft(out[i+2:], "0123456789;")
		if len(params) > 0 && params[0] == 'q' {
			return true
		}
		next := bytes.Index(out[i+2:], []byte("\x1bP"))
		if next < 0 {
			return false
		}
		i += 2 + next
	}
	return false
}

// conptyTestDirEnv names a folder holding the conpty.dll and OpenConsole.exe
// of the Microsoft.Windows.Console.ConPTY package (x64 runtime). The
// repository does not carry them, so the tests that need them are opt-in.
const conptyTestDirEnv = "TUIOS_E2E_CONPTY_DIR"

// TestPaneUsesConPTYDLLWhenPresent is the positive half of the fallback
// tests: with the real DLL in the folder, the pane opens on it, the log names
// it, and a sixel image the guest draws reaches tuios, which the inbox ConPTY
// drops.
func TestPaneUsesConPTYDLLWhenPresent(t *testing.T) {
	dir := os.Getenv(conptyTestDirEnv)
	if dir == "" {
		t.Skipf("set %s to a folder holding %s and %s from the Microsoft.Windows.Console.ConPTY package", conptyTestDirEnv, conptyDLLName, openConsoleName)
	}
	useConPTYDLL(t, dir)

	// One sixel pixel column, six rows high, in colour 0.
	draw := `[Console]::Out.Write([char]27 + 'Pq#0;2;100;0;0#0~~~~' + [char]27 + '\'); [Console]::Out.Flush(); Start-Sleep -Seconds 2`
	pty, logged, out := spawnUntil(t, "a sixel image", command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", draw), hasSixel)
	if _, ok := pty.(*dllPty); !ok {
		t.Fatalf("pane pty is %T, want *dllPty on %s", pty, dir)
	}
	if len(logged) != 1 || !strings.Contains(logged[0], "pane on the pseudo console of "+filepath.Join(dir, conptyDLLName)) {
		t.Fatalf("log = %q, want one line naming %s", logged, filepath.Join(dir, conptyDLLName))
	}
	sixel := out[bytes.Index(out, []byte("\x1bP")):]
	t.Logf("sixel reached the master side: %q", sixel[:min(len(sixel), 32)])
}

// TestPaneOnConPTYDLLFindsProgramLikeInbox starts a program given relative
// to cmd.Dir, or rooted on its drive, on both ConPTYs. CreateProcess
// resolves a relative program against the daemon's current directory, not
// cmd.Dir, so each pane has to make the path absolute itself, and the DLL
// pane has to do it as the inbox pane does.
func TestPaneOnConPTYDLLFindsProgramLikeInbox(t *testing.T) {
	dllDir := os.Getenv(conptyTestDirEnv)
	if dllDir == "" {
		t.Skipf("set %s to a folder holding %s and %s from the Microsoft.Windows.Console.ConPTY package", conptyTestDirEnv, conptyDLLName, openConsoleName)
	}
	root := os.Getenv("SystemRoot") // C:\Windows
	vol := filepath.VolumeName(root)
	// A cmd.Dir on the program's drive, under which the rooted path joined
	// as a relative one names nothing.
	elsewhere := filepath.Join(root, "System32")
	forms := []struct{ name, dir, path string }{
		{"relative", root, `System32\cmd.exe`},
		{"relative without extension", root, `System32\cmd`},
		{"rooted on the drive", elsewhere, root[len(vol):] + `\System32\cmd.exe`},
	}
	for _, pane := range []struct {
		name, dir string
		want      func(xpty.Pty) bool
	}{
		{"inbox", t.TempDir(), func(p xpty.Pty) bool { _, ok := p.(*xpty.ConPty); return ok }},
		{"dll", dllDir, func(p xpty.Pty) bool { _, ok := p.(*dllPty); return ok }},
	} {
		for _, form := range forms {
			t.Run(pane.name+"/"+form.name, func(t *testing.T) {
				useConPTYDLL(t, pane.dir)
				marker := "program-found-marker"
				pty, _, _ := spawnUntil(t, marker, func() *exec.Cmd {
					cmd := exec.Command(form.path, "/c", "echo "+marker)
					cmd.Dir = form.dir
					return cmd
				}, printed(marker))
				if !pane.want(pty) {
					t.Fatalf("pane pty is %T, want the %s one", pty, pane.name)
				}
			})
		}
	}
}

func TestConPTYDLLIsNotTakenFromWorkingDirOrPath(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		t.Fatal(err)
	}
	beside := filepath.Dir(exe)
	for _, name := range []string{conptyDLLName, openConsoleName} {
		if _, err := os.Stat(filepath.Join(beside, name)); err == nil {
			t.Skipf("the test binary's folder %s already holds a %s, which this test would replace", beside, name)
		}
	}

	planted := t.TempDir()
	garbageDLL(t, planted)
	t.Chdir(planted)
	t.Setenv("PATH", planted+string(os.PathListSeparator)+os.Getenv("PATH"))

	// The test binary's folder has no conpty.dll, so the lookup must find
	// nothing at all. Finding the planted file shows up as a load error.
	dll, err := exeConPTYDLL()
	if dll != nil || err != nil {
		t.Fatalf("lookup beside the executable found something: dll=%v err=%v", dll, err)
	}

	// The positive half: the same files beside the test binary are found,
	// so the lookup above did look, and looked in the right folder. They
	// have to be written there, the only folder the lookup reads; the skip
	// above keeps this from replacing files already there.
	garbageDLL(t, beside)
	t.Cleanup(func() {
		_ = os.Remove(filepath.Join(beside, conptyDLLName))
		_ = os.Remove(filepath.Join(beside, openConsoleName))
	})
	dll, err = exeConPTYDLL()
	if dll != nil || err == nil || !strings.Contains(err.Error(), "failed to load "+filepath.Join(beside, conptyDLLName)) {
		t.Fatalf("lookup did not find the conpty.dll beside the executable: dll=%v err=%v", dll, err)
	}
}

func TestPaneUsesInboxConPTYWithoutDLL(t *testing.T) {
	useConPTYDLL(t, t.TempDir())

	pty, logged := spawnEcho(t, "inbox-marker")
	if _, ok := pty.(*xpty.ConPty); !ok {
		t.Fatalf("pane pty is %T, want the inbox *xpty.ConPty", pty)
	}
	if len(logged) != 0 {
		t.Fatalf("log = %q, want nothing: a pane with no DLL to try is not an exception", logged)
	}
}

func TestPaneFallsBackToInboxWhenDLLWillNotLoad(t *testing.T) {
	dir := t.TempDir()
	garbageDLL(t, dir)
	useConPTYDLL(t, dir)

	pty, logged := spawnEcho(t, "fallback-marker")
	if _, ok := pty.(*xpty.ConPty); !ok {
		t.Fatalf("pane pty is %T, want the inbox *xpty.ConPty", pty)
	}
	if len(logged) != 1 || !strings.Contains(logged[0], "failed to load "+filepath.Join(dir, conptyDLLName)) || !strings.Contains(logged[0], "inbox ConPTY") {
		t.Fatalf("log = %q, want one line naming the refused DLL and the inbox ConPTY", logged)
	}
}

func TestPaneFallsBackToInboxWithoutOpenConsole(t *testing.T) {
	dir := t.TempDir()
	garbageDLL(t, dir)
	if err := os.Remove(filepath.Join(dir, openConsoleName)); err != nil {
		t.Fatal(err)
	}
	useConPTYDLL(t, dir)

	pty, logged := spawnEcho(t, "no-openconsole-marker")
	if _, ok := pty.(*xpty.ConPty); !ok {
		t.Fatalf("pane pty is %T, want the inbox *xpty.ConPty", pty)
	}
	if len(logged) != 1 || !strings.Contains(logged[0], openConsoleName) || !strings.Contains(logged[0], "inbox ConPTY") {
		t.Fatalf("log = %q, want one line naming the missing %s and the inbox ConPTY", logged, openConsoleName)
	}
}
