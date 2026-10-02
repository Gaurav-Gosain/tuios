package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// Which frames a client that is behind may skip. The rules are in
// internal/session/kitty_frames.go. These tests stop a real client with
// SIGSTOP while a real guest writes, and compare what it shows when it reads
// again with what a client that kept up shows.

// TestKittySkippedFrameKeepsTheScroll: an a=T without C=1 at the bottom row
// scrolls the pane. A cursor move and a newer frame of the same image follow.
// A client that was stopped through all of it must show the same rows as one
// that kept up: the scroll is the frame's, and a cursor move does not undo it.
func TestKittySkippedFrameKeepsTheScroll(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "scroll", "--detach"); err != nil {
		t.Fatalf("create the session: %v: %s", err, out)
	}
	fast := attachPacingClient(t, base, "scroll")
	slow := attachPacingClient(t, base, "scroll")
	t.Cleanup(slow.resume)
	time.Sleep(insertGuard + 150*time.Millisecond)
	runInShell(t, fast.term, "echo SCR\"\"OLL", "SCROLL", shellTimeout)

	dir := t.TempDir()
	goFile := filepath.Join(dir, "go")
	script := filepath.Join(dir, "s.sh")
	img := `\033_Ga=T,f=32,s=1,v=1,i=7,r=3,c=2,q=2;/wAA/w==\033\\`
	// 1.5 MB of text first, so the stopped client is behind when the frames
	// come.
	body := fmt.Sprintf(`L=$(tput lines)
while [ ! -f %s ]; do sleep 0.05; done
head -c 1500000 /dev/zero | tr '\0' 'x'
printf '\033[2J\033[H'
i=1; while [ $i -lt $L ]; do printf 'row%%02d\r\n' $i; i=$((i+1)); done
printf "\033[${L};1H%s"
sleep 0.3
printf "\033[${L};1H%s"
sleep 0.3
printf '\033[1;40HDO''NE'
sleep 30
`, goFile, img, img)
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	typeLine(t, fast.term, "clear; sh "+script)
	time.Sleep(500 * time.Millisecond)
	slow.stop(t)
	if err := os.WriteFile(goFile, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := fast.term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "DONE") }, 10*time.Second); err != nil {
		t.Fatalf("the client that kept up never showed DONE\n%s", fast.term.Snapshot())
	}
	time.Sleep(300 * time.Millisecond)
	slow.resume()
	if err := slow.term.WaitFor(func(s tuitest.Screen) bool { return strings.Contains(s.Text(), "DONE") }, 10*time.Second); err != nil {
		t.Fatalf("the stopped client never showed DONE\n%s", slow.term.Snapshot())
	}
	time.Sleep(time.Second)
	rowRE := regexp.MustCompile(`row\d\d`)
	fr := rowRE.FindAllString(fast.term.Screen().Text(), -1)
	sr := rowRE.FindAllString(slow.term.Screen().Text(), -1)
	t.Logf("daemon: %s", strings.Join(pacingLog(t, base), "\n  "))
	t.Logf("rows, kept up: %v", fr)
	t.Logf("rows, stopped: %v", sr)
	if len(fr) == 0 {
		t.Fatalf("the client that kept up shows no rows\n%s", fast.term.Snapshot())
	}
	if strings.Join(fr, ",") != strings.Join(sr, ",") {
		t.Errorf("the two clients show different rows in one pane\nkept up:\n%s\nstopped:\n%s", fast.term.Snapshot(), slow.term.Snapshot())
	}
}

// startMpvGuest starts a session with one pane and runs the frameloop guest
// in it the way mpv's --vo=kitty draws: every frame a new image with no id,
// at the same cell.
func startMpvGuest(t *testing.T, clients int) (base string, cs []*pacingClient, count string) {
	t.Helper()
	base = t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "mpv", "--detach"); err != nil {
		t.Fatalf("create the session: %v: %s", err, out)
	}
	for range clients {
		cs = append(cs, attachPacingClient(t, base, "mpv"))
	}
	time.Sleep(insertGuard + 150*time.Millisecond)
	runInShell(t, cs[0].term, "echo MP\"\"V", "MPV", shellTimeout)
	bin := buildFrameloop(t)
	dir := t.TempDir()
	count = filepath.Join(dir, "count")
	typeLine(t, cs[0].term, fmt.Sprintf("%s %s 30 0 mpv %s", bin, filepath.Join(dir, "geom"), count))
	deadline := time.Now().Add(shellTimeout)
	for guestFrames(count) < 5 {
		if time.Now().After(deadline) {
			t.Fatalf("the guest never wrote a frame\n%s", cs[0].term.Snapshot())
		}
		time.Sleep(50 * time.Millisecond)
	}
	return base, cs, count
}

// TestKittyStuckClientSkipsIDlessFrames: two clients on a pane that streams
// mpv's frames, and one of them stops for 3 s. Its stream is not cut: it
// skips the frames each newer one is drawn over, and prints no image data
// when it reads again.
func TestKittyStuckClientSkipsIDlessFrames(t *testing.T) {
	base, cs, count := startMpvGuest(t, 2)
	stuck := cs[1]
	t.Cleanup(stuck.resume)
	g0 := guestFrames(count)
	stuck.stop(t)
	time.Sleep(3 * time.Second)
	stuck.resume()
	time.Sleep(3 * time.Second)
	log := pacingLog(t, base)
	t.Logf("daemon: %s", strings.Join(log, "\n  "))
	tot := sumPacing(log)
	garbage := base64Rows(stuck.term.Screen().Text())
	t.Logf("guest wrote %d frames; %d skipped, %d streams cut, %d rows of base64 on the stopped client",
		guestFrames(count)-g0, tot.skipped, tot.cut, len(garbage))
	if tot.skipped == 0 {
		t.Errorf("the stopped client skipped no frames")
	}
	if tot.cut > 0 {
		t.Errorf("the daemon cut %d client streams: the stopped client fell behind instead of skipping frames", tot.cut)
	}
	if len(garbage) > 0 {
		t.Errorf("image data was printed into the stopped client's pane as text:\n%s", strings.Join(garbage, "\n"))
	}
}

// TestKittyAttachDuringIDlessStreamPrintsNoImageData: clients that attach
// while a pane streams mpv's frames start after the frame in progress, so
// none of them prints the rest of it as text.
func TestKittyAttachDuringIDlessStreamPrintsNoImageData(t *testing.T) {
	base, _, _ := startMpvGuest(t, 1)
	const attaches = 16
	bad := 0
	for i := range attaches {
		c := attachPacingClient(t, base, "mpv")
		time.Sleep(700 * time.Millisecond)
		if garbage := base64Rows(c.term.Screen().Text()); len(garbage) > 0 {
			bad++
			if bad == 1 {
				t.Logf("attach %d printed image data:\n%s", i+1, strings.Join(garbage, "\n"))
			}
		}
		_ = c.term.Close()
	}
	if bad > 0 {
		t.Errorf("%d of %d attaches printed image data as text", bad, attaches)
	}
}
