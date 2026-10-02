package tuie2e

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// A kitty guest that streams over shared memory hands each frame's object to
// the terminal, and the spec makes the terminal delete it after reading. tuios
// forwards the name to the host, which deletes what it reads. Every frame tuios
// does not forward has no other reader, so tuios has to delete it, or it stays
// in /dev/shm, which is memory. A compositor streaming into a pane left 3,863
// such objects, 5.3 GB, behind.
//
// The stand-in in ./shmstream advertises a new object per frame and never
// deletes one. The host here records and never reads, so an object whose name
// reached it is the host's to delete and is excused. Every other object must be
// gone once the stream ends.
func TestKittySharedMemoryFramesAreReleased(t *testing.T) {
	requireDevShm(t)

	// Frames that repeat the picture on screen: tuios forwards the first and
	// drops the rest as identical, unread by anyone.
	t.Run("repeats", func(t *testing.T) {
		host := newKittyHost()
		term, _ := start(t, startOpts{
			cols: 120, rows: 40,
			env: []string{"TUIOS_SIXEL_GRAPHICS=0"},
			out: host,
		})
		host.answerProbe(t, term)
		waitBoot(t, term)
		newWindow(t, term)
		enableTiling(t, term)
		waitWindowCount(t, term, 1, "one pane")
		enterTerminalMode(t, term)
		runInShell(t, term, "echo IMAG\"\"EPANE", "IMAGEPANE", shellTimeout)

		names := runShmStream(t, term, 40, 20, 0, "same")
		forwarded := forwardedShmNames(host.bytes())
		left := leftInDevShm(names)
		t.Logf("%d frames: %d forwarded to the host, %d still in /dev/shm", len(names), len(forwarded), len(left))
		// The positive half: the stream did reach the host, and most frames
		// were dropped, so the drop path is the one under test.
		if len(forwarded) == 0 {
			t.Fatalf("no frame was forwarded to the host, so nothing here exercised the passthrough")
		}
		if len(names)-len(forwarded) < len(names)/2 {
			t.Fatalf("only %d of %d frames were dropped; the repeats were not recognised", len(names)-len(forwarded), len(names))
		}
		assertReleased(t, left, forwarded)
	})

	// A daemon pane on a workspace that is not shown has no client reading
	// it, so the daemon is the last reader of every frame.
	t.Run("daemon-unwatched", func(t *testing.T) {
		names, forwarded, left := streamUnwatched(t, "vary")
		t.Logf("%d frames: %d forwarded to the host, %d still in /dev/shm", len(names), len(forwarded), len(left))
		assertReleased(t, left, forwarded)
	})

	// The same stream with each object one byte longer than the frame its
	// command describes. The name is text the pane printed, and an object of
	// another size may be another program's, so the daemon keeps every one.
	// "daemon-unwatched" above is the positive half: the same path deletes
	// objects of the right size.
	t.Run("daemon-unwatched-wrong-size", func(t *testing.T) {
		names, forwarded, left := streamUnwatched(t, "big")
		t.Logf("%d frames: %d forwarded to the host, %d still in /dev/shm", len(names), len(forwarded), len(left))
		if len(left) != len(names) {
			t.Fatalf("%d of %d objects of the wrong size were deleted", len(names)-len(left), len(names))
		}
	})
}

// streamUnwatched runs the stand-in in a daemon pane on a workspace that is
// not shown, and returns the names it advertised, the names the host was
// handed, and the names still in /dev/shm.
func streamUnwatched(t *testing.T, paint string) ([]string, map[string]bool, []string) {
	t.Helper()
	host := newKittyHost()
	term, base := start(t, startOpts{
		cols: 120, rows: 40,
		env:           []string{"TUIOS_SIXEL_GRAPHICS=0"},
		out:           host,
		daemonDefault: true,
	})
	killDaemon(t, base)
	host.answerProbe(t, term)
	waitBoot(t, term)
	newWindow(t, term)
	enterTerminalMode(t, term)
	runInShell(t, term, "echo IMAG\"\"EPANE", "IMAGEPANE", shellTimeout)

	logPath := filepath.Join(t.TempDir(), "names")
	typeLine(t, term, fmt.Sprintf("%s=%s %s %s 40 20 3000 %s", shmPrefixEnv, standInShmPrefix(t), buildShmStream(t), logPath, paint))
	leaveTerminalMode(t, term)
	if err := term.SendKeys(tuitest.Ctrl('b'), "w", "2"); err != nil {
		t.Fatalf("switch to workspace 2: %v", err)
	}
	if err := term.WaitFor(func(s tuitest.Screen) bool {
		return !strings.Contains(s.Text(), "IMAGEPANE")
	}, uiTimeout); err != nil {
		t.Fatalf("workspace 2 still shows the image pane: %v\n%s", err, term.Snapshot())
	}
	host.mark("hidden")
	names := waitShmStream(t, term, logPath)
	time.Sleep(time.Second)
	return names, forwardedShmNames(host.bytes()), leftInDevShm(names)
}

// assertReleased fails for every object still in /dev/shm whose name never
// reached the host.
func assertReleased(t *testing.T, left []string, forwarded map[string]bool) {
	t.Helper()
	var leaked []string
	for _, name := range left {
		if !forwarded[name] {
			leaked = append(leaked, name)
		}
	}
	if len(leaked) > 0 {
		t.Fatalf("%d shared memory objects were never sent to the host and never deleted:\n  %s",
			len(leaked), strings.Join(uniq(leaked), "\n  "))
	}
}

// runShmStream runs the stand-in in the focused pane and waits for it to finish.
func runShmStream(t *testing.T, term *tuitest.Terminal, count, fps, delayMS int, paint string) []string {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "names")
	typeLine(t, term, fmt.Sprintf("%s=%s %s %s %d %d %d %s", shmPrefixEnv, standInShmPrefix(t), buildShmStream(t), logPath, count, fps, delayMS, paint))
	names := waitShmStream(t, term, logPath)
	// The last frames are still on their way through the render loop.
	time.Sleep(time.Second)
	return names
}

// waitShmStream waits for the stand-in to log DONE and returns the names it
// advertised. Every one of them is removed when the test ends, whatever the
// test found, so a failing run does not leave memory behind.
func waitShmStream(t *testing.T, term *tuitest.Terminal, logPath string) []string {
	t.Helper()
	deadline := time.Now().Add(shellTimeout + 10*time.Second)
	for time.Now().Before(deadline) {
		b, _ := os.ReadFile(logPath)
		lines := strings.Fields(string(b))
		if len(lines) > 0 && lines[len(lines)-1] == "DONE" {
			names := lines[:len(lines)-1]
			t.Cleanup(func() {
				for _, n := range names {
					_ = os.Remove("/dev/shm/" + n)
				}
			})
			return names
		}
		time.Sleep(100 * time.Millisecond)
	}
	b, _ := os.ReadFile(logPath)
	for _, n := range strings.Fields(string(b)) {
		_ = os.Remove("/dev/shm/" + n)
	}
	t.Fatalf("the stream never finished\n%s", term.Snapshot())
	return nil
}

// leftInDevShm is the names that are still objects in /dev/shm.
func leftInDevShm(names []string) []string {
	var out []string
	for _, n := range names {
		if _, err := os.Lstat("/dev/shm/" + n); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// shmPayloadRE matches a kitty command that names a shared memory object, and
// its base64 payload.
var shmPayloadRE = regexp.MustCompile(`\x1b_G([^;\x1b]*t=s[^;\x1b]*);([A-Za-z0-9+/=]*)\x1b\\`)

// forwardedShmNames is every shared memory name the host was handed.
func forwardedShmNames(stream []byte) map[string]bool {
	out := map[string]bool{}
	for _, m := range shmPayloadRE.FindAllSubmatch(stream, -1) {
		if name, err := base64.StdEncoding.DecodeString(string(m[2])); err == nil {
			out[strings.TrimPrefix(string(name), "/")] = true
		}
	}
	return out
}

// buildShmStream compiles the shared memory stand-in once per test binary.
func buildShmStream(t *testing.T) string {
	t.Helper()
	shmStreamOnce.Do(func() {
		dir, err := os.MkdirTemp("", "shmstream")
		if err != nil {
			shmStreamErr = err
			return
		}
		bin := filepath.Join(dir, "shmstream")
		build := exec.Command("go", "build", "-o", bin, "./shmstream")
		if out, err := build.CombinedOutput(); err != nil {
			shmStreamErr = fmt.Errorf("build shmstream: %v\n%s", err, out)
			return
		}
		shmStreamBin = bin
	})
	if shmStreamErr != nil {
		t.Fatalf("%v", shmStreamErr)
	}
	return shmStreamBin
}

var (
	shmStreamOnce sync.Once
	shmStreamBin  string
	shmStreamErr  error
)
