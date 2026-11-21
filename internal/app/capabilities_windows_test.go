//go:build windows

package app

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	xwindows "github.com/charmbracelet/x/windows"
	"github.com/charmbracelet/x/xpty"
	"golang.org/x/sys/windows"
	"golang.org/x/term"

	"github.com/Gaurav-Gosain/tuios/internal/ptyspawn"
)

const probeChildEnv = "TUIOS_TEST_PROBE_CHILD"

const conptyDirEnv = "TUIOS_E2E_CONPTY_DIR"

const probeStagedEnv = "TUIOS_TEST_PROBE_STAGED"

// TestHostProbeAsksTheWindowsConsole checks that the probe asks the host
// terminal on Windows. It used to open /dev/tty, which Windows does not have,
// so it asked nothing and a host with sixel was taken for one without (#1107).
//
// The inbox console answers DA1 itself, without sixel, so on it the probe
// stops before the host's answers arrive, and only the asking is tested. The
// OpenConsole of a conpty.dll passes the queries on, as Windows Terminal's
// does, so with the ConPTY package the host answers and the child checks what
// the probe made of it.
func TestHostProbeAsksTheWindowsConsole(t *testing.T) {
	if os.Getenv(probeChildEnv) != "" {
		t.Skip("this is the child on the pseudo console")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	mode := "inbox"
	if dir := os.Getenv(conptyDirEnv); dir != "" {
		if os.Getenv(probeStagedEnv) == "" {
			runStaged(t, exe, dir)
			return
		}
		mode = "host"
	}
	pty, cmd, err := ptyspawn.Spawn(200, 30, func() *exec.Cmd {
		c := exec.Command(exe, "-test.run=^TestHostProbeChild$", "-test.v", "-test.count=1")
		c.Env = append(os.Environ(), probeChildEnv+"="+mode)
		return c
	}, func(string, ...any) {})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = pty.Close()
	})
	if _, inbox := pty.(*xpty.ConPty); inbox && mode == "host" {
		t.Fatalf("the pane opened on the inbox ConPTY, not on the conpty.dll beside %s", exe)
	}

	got := make(chan []byte, 1)
	var partial atomic.Value
	go func() {
		var out []byte
		var cells, da1s int
		buf := make([]byte, 4096)
		for {
			n, err := pty.Read(buf)
			out = append(out, buf[:n]...)
			partial.Store(string(out))
			for ; cells < bytes.Count(out, []byte("\x1b[16t")); cells++ {
				_, _ = pty.Write([]byte("\x1b[6;19;9t"))
			}
			for ; da1s < bytes.Count(out, []byte("\x1b[c")); da1s++ {
				_, _ = pty.Write([]byte("\x1b[?62;4c"))
			}
			if err != nil || bytes.Contains(out, []byte("--- PASS: TestHostProbeChild")) ||
				bytes.Contains(out, []byte("--- FAIL: TestHostProbeChild")) {
				got <- out
				return
			}
		}
	}()

	var out []byte
	select {
	case out = <-got:
	case <-time.After(time.Minute):
		t.Fatalf("the child on the pseudo console never finished; it printed %q", partial.Load())
	}
	if !bytes.Contains(out, []byte("\x1b[16t")) {
		t.Errorf("the probe never wrote its queries to the console; the host saw %q", out)
	}
	if !bytes.Contains(out, []byte("--- PASS: TestHostProbeChild")) {
		t.Errorf("the child failed:\n%s", out)
	}
}

func runStaged(t *testing.T, exe, dir string) {
	t.Helper()
	stage := t.TempDir()
	staged := filepath.Join(stage, filepath.Base(exe))
	for _, c := range [][2]string{
		{exe, staged},
		{filepath.Join(dir, "conpty.dll"), filepath.Join(stage, "conpty.dll")},
		{filepath.Join(dir, "OpenConsole.exe"), filepath.Join(stage, "OpenConsole.exe")},
	} {
		data, err := os.ReadFile(c[0])
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(c[1], data, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(staged, "-test.run=^TestHostProbeAsksTheWindowsConsole$", "-test.v", "-test.count=1")
	cmd.Env = append(os.Environ(), probeStagedEnv+"=1")
	out, err := cmd.CombinedOutput()
	if err != nil || !bytes.Contains(out, []byte("--- PASS: TestHostProbeAsksTheWindowsConsole")) {
		t.Fatalf("the test failed on the conpty.dll from %s (%v):\n%s", dir, err, out)
	}
}

// TestHostProbeChild runs on the pseudo console that
// TestHostProbeAsksTheWindowsConsole opens. Its failures reach the parent as
// text.
func TestHostProbeChild(t *testing.T) {
	if os.Getenv(probeChildEnv) == "" {
		t.Skip("runs only on the pseudo console of TestHostProbeAsksTheWindowsConsole")
	}

	in, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.OpenFile("CONOUT$", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	outMode := consoleMode(t, out) &^ windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING
	if err := windows.SetConsoleMode(windows.Handle(out.Fd()), outMode); err != nil {
		t.Fatal(err)
	}
	inMode := consoleMode(t, in)

	caps := &HostCapabilities{}
	probeTerminal(caps)
	if caps.DA1Late {
		t.Fatal("the probe read no DA1 reply from the console")
	}
	if got := consoleMode(t, out); got != outMode {
		t.Errorf("the probe left the output mode %#x, want %#x", got, outMode)
	}
	if got := consoleMode(t, in); got != inMode {
		t.Errorf("the probe left the input mode %#x, want %#x", got, inMode)
	}
	if os.Getenv(probeChildEnv) == "host" {
		if !caps.SixelGraphics {
			t.Error("the host answered DA1 with sixel, and the probe took it for a terminal without")
		}
		if caps.CellWidth != 9 || caps.CellHeight != 19 {
			t.Errorf("cell = %dx%d, want the host's 9x19", caps.CellWidth, caps.CellHeight)
		}
	}

	old, err := term.MakeRaw(int(in.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = term.Restore(int(in.Fd()), old) }()
	handle := windows.Handle(in.Fd())
	time.Sleep(200 * time.Millisecond)
	if err := xwindows.FlushConsoleInputBuffer(handle); err != nil {
		t.Fatal(err)
	}

	writeConsoleInput(t, handle, focusRecord())
	const timeout = 300 * time.Millisecond
	reply, took, ok := readWithin(in, timeout, 5*time.Second)
	switch {
	case !ok:
		t.Fatal("the read blocked on a focus record past its timeout")
	case reply != "":
		t.Fatalf("the read returned %q for a focus record", reply)
	case took > 2*timeout:
		t.Fatalf("the read gave up after %v on a focus record, past its %v timeout", took, timeout)
	}

	stop, wrote := make(chan struct{}), make(chan int, 1)
	go func() {
		n := 0
		defer func() { wrote <- n }()
		batch := slices.Repeat([]xwindows.InputRecord{focusRecord()}, 1024)
		for end := time.Now().Add(2 * time.Second); time.Now().Before(end); {
			select {
			case <-stop:
				return
			default:
			}
			var queued uint32
			if windows.GetNumberOfConsoleInputEvents(handle, &queued) == nil && queued < 8192 &&
				writeRecords(handle, batch) == nil {
				n++
			}
			time.Sleep(time.Millisecond)
		}
	}()
	reply, took, ok = readWithin(in, timeout, 5*time.Second)
	close(stop)
	switch {
	case <-wrote == 0:
		t.Fatal("no focus records were written to the console, so the stream was not tested")
	case !ok:
		t.Fatal("the read blocked on a stream of focus records past its timeout")
	case reply != "":
		t.Fatalf("the read returned %q for a stream of focus records", reply)
	case took > 2*timeout:
		t.Fatalf("the read gave up after %v on a stream of focus records, past its %v timeout", took, timeout)
	}
	if err := xwindows.FlushConsoleInputBuffer(handle); err != nil {
		t.Fatal(err)
	}

	writeConsoleInput(t, handle, keyRecord('x', true), keyRecord('x', false))
	if reply, _, ok := readWithin(in, time.Second, 5*time.Second); !ok || reply != "x" {
		t.Fatalf("the read returned %q (finished %v), want %q", reply, ok, "x")
	}
}

// TestPollReadableGivesUpAtAnExpiredDeadline: the deadline can pass between
// pollReadable's check of it and the next wait, which leaves a wait below
// zero. Made unsigned, that was a 49-day wait on a handle nothing signals.
func TestPollReadableGivesUpAtAnExpiredDeadline(t *testing.T) {
	event, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(event)
	done := make(chan bool, 1)
	go func() {
		ready, _ := pollReadable(uintptr(event), -time.Millisecond)
		done <- ready
	}()
	select {
	case ready := <-done:
		if ready {
			t.Error("a handle nothing signalled was taken as readable")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the wait at an expired deadline did not return")
	}
}

func consoleMode(t *testing.T, f *os.File) uint32 {
	t.Helper()
	var mode uint32
	if err := windows.GetConsoleMode(windows.Handle(f.Fd()), &mode); err != nil {
		t.Fatal(err)
	}
	return mode
}

func readWithin(in *os.File, timeout, limit time.Duration) (string, time.Duration, bool) {
	done := make(chan string, 1)
	start := time.Now()
	go func() { done <- readTTYResponse(in, timeout, func(s string) bool { return s != "" }) }()
	select {
	case s := <-done:
		return s, time.Since(start), true
	case <-time.After(limit):
		return "", 0, false
	}
}

func focusRecord() xwindows.InputRecord {
	r := xwindows.InputRecord{EventType: windows.FOCUS_EVENT}
	r.Event[0] = 1
	return r
}

func keyRecord(char rune, down bool) xwindows.InputRecord {
	r := xwindows.InputRecord{EventType: windows.KEY_EVENT}
	if down {
		binary.LittleEndian.PutUint32(r.Event[0:4], 1)
	}
	binary.LittleEndian.PutUint16(r.Event[4:6], 1)
	binary.LittleEndian.PutUint16(r.Event[10:12], uint16(char))
	return r
}

var procWriteConsoleInput = windows.NewLazySystemDLL("kernel32.dll").NewProc("WriteConsoleInputW")

func writeConsoleInput(t *testing.T, handle windows.Handle, records ...xwindows.InputRecord) {
	t.Helper()
	if err := writeRecords(handle, records); err != nil {
		t.Fatal(err)
	}
}

func writeRecords(handle windows.Handle, records []xwindows.InputRecord) error {
	var written uint32
	r, _, err := procWriteConsoleInput.Call(uintptr(handle), uintptr(unsafe.Pointer(&records[0])),
		uintptr(len(records)), uintptr(unsafe.Pointer(&written)))
	if r == 0 {
		return fmt.Errorf("WriteConsoleInput: %w", err)
	}
	if int(written) != len(records) {
		return fmt.Errorf("WriteConsoleInput wrote %d of %d records", written, len(records))
	}
	return nil
}
