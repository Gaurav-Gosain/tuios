package session

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

// Cases the property test (FuzzFrameSkipping) cannot reach: shared memory
// objects on disk, catch-ups from the ring, and a stall long enough to fill a
// client's queue.

// TestSkippedFrameNeverLeaksSharedMemory: a stream that sends each frame as a
// new shared memory object (t=s) under one image id. The terminal that reads
// an object deletes it, so a frame dropped for a slow client would leave its
// object in /dev/shm. The same stream sent in the escape code (t=d) is
// skipped, so the fixture does make the client fall behind.
func TestSkippedFrameNeverLeaksSharedMemory(t *testing.T) {
	if _, err := os.Stat("/dev/shm"); err != nil {
		t.Skip("no /dev/shm")
	}
	nameRE := regexp.MustCompile(`\x1b_Ga=T,f=32,t=s[^;]*;([A-Za-z0-9+/=]+)\x1b\\`)
	for _, medium := range []string{"s", "d"} {
		t.Run("t="+medium, func(t *testing.T) {
			p := queuePTY()
			ch := p.Subscribe("slow", 0)
			sub := p.subscriberFor("slow")
			var created []string
			var got []byte
			for n := range 60 {
				name := fmt.Sprintf("tuios-skip-test-%d-%d", os.Getpid(), n)
				path := "/dev/shm/" + name
				if err := os.WriteFile(path, []byte("pixels"), 0o600); err != nil {
					t.Fatal(err)
				}
				created = append(created, path)
				t.Cleanup(func() { _ = os.Remove(path) })
				payload := base64.StdEncoding.EncodeToString([]byte(name))
				p.feedRing(fmt.Appendf(nil, "\x1b[H\x1b_Ga=T,f=32,t=%s,s=1,v=1,i=1,q=2,C=1;%s\x1b\\", medium, payload))
				if n%10 == 9 {
					for len(ch) > 0 {
						got = takeChunk(got, <-ch, sub)
					}
				}
			}
			for len(ch) > 0 {
				got = takeChunk(got, <-ch, sub)
			}
			skipped := p.framesSkipped.Load()
			if medium == "d" {
				if skipped == 0 {
					t.Fatal("no frame sent in the escape code was skipped: the client never fell behind")
				}
				return
			}
			// Act as the terminal: delete each object the client was sent.
			for _, m := range nameRE.FindAllSubmatch(got, -1) {
				name, err := base64.StdEncoding.DecodeString(string(m[1]))
				if err != nil {
					t.Fatal(err)
				}
				_ = os.Remove("/dev/shm/" + string(name))
			}
			leaked := 0
			for _, path := range created {
				if _, err := os.Stat(path); err == nil {
					leaked++
				}
			}
			if leaked > 0 || skipped > 0 {
				t.Fatalf("%d frames skipped, %d of %d shared memory objects left in /dev/shm", skipped, leaked, len(created))
			}
		})
	}
}

// mpvFrame is one frame as mpv's --vo=kitty sends it: a cursor move, then an
// image with no id in 4 KiB chunks. keys replaces mpv's keys when not empty.
func mpvFrame(n int, cup, keys string) []byte {
	if keys == "" {
		keys = "a=T,f=24,s=100,v=100,C=1,q=2"
	}
	var b bytes.Buffer
	b.WriteString(cup)
	writeFrame(&b, keys, strings.Repeat(string(rune('A'+n%26)), 64<<10), 16)
	return b.Bytes()
}

// TestIDlessFramesAreSkipped: a client that takes nothing while a pane
// streams frames with no image id, as mpv does. A frame is skipped when the
// next one is drawn exactly over it, so the client's queue never fills. A
// frame that something else might show through is never skipped.
func TestIDlessFramesAreSkipped(t *testing.T) {
	prev := maxSubscriberQueue
	maxSubscriberQueue = 1 << 20
	t.Cleanup(func() { maxSubscriberQueue = prev })

	cases := []struct {
		name  string
		frame func(n int) []byte
		skips bool
	}{
		{"mpv", func(n int) []byte { return mpvFrame(n, "\x1b[H", "") }, true},
		{"mpv with f", func(n int) []byte { return mpvFrame(n, "\x1b[2;3f", "") }, true},
		{"alpha", func(n int) []byte { return mpvFrame(n, "\x1b[H", "a=T,f=32,s=100,v=100,C=1,q=2") }, false},
		{"two places", func(n int) []byte { return mpvFrame(n, fmt.Sprintf("\x1b[%dH", 1+n%2), "") }, false},
		{"other sizes", func(n int) []byte {
			return mpvFrame(n, "\x1b[H", fmt.Sprintf("a=T,f=24,s=%d,v=100,C=1,q=2", 100+n%2))
		}, false},
		{"no cursor move", func(n int) []byte { return mpvFrame(n, "", "") }, false},
		{"text between", func(n int) []byte { return mpvFrame(n, "x\x1b[H", "") }, false},
		{"moves the cursor", func(n int) []byte { return mpvFrame(n, "\x1b[H", "a=T,f=24,s=100,v=100,q=2") }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := queuePTY()
			_ = p.Subscribe("stuck", 0)
			for n := range 40 {
				p.feedReads(c.frame(n))
			}
			skipped, cut := p.framesSkipped.Load(), p.streamsCut.Load()
			if c.skips && (skipped == 0 || cut > 0) {
				t.Fatalf("%d frames skipped, %d streams cut: want frames skipped and no cut", skipped, cut)
			}
			if !c.skips && skipped > 0 {
				t.Fatalf("%d frames skipped: none of these may replace another", skipped)
			}
		})
	}
}

// TestCatchUpNeverStartsInsideAFrame: a stream of small frames puts hundreds
// of graphics commands in the ring. A client that subscribes from the ring's
// start must not start inside one of them, however many came after it.
func TestCatchUpNeverStartsInsideAFrame(t *testing.T) {
	b64 := regexp.MustCompile(`[A-Za-z0-9+/]{30,}`)
	bad := 0
	for trial := range 50 {
		p := queuePTY()
		var b bytes.Buffer
		for n := range 400 + trial*7 {
			b.WriteString("\x1b[H")
			writeFrame(&b, "a=T,f=32,s=1,v=1,i=1,C=1,q=2", strings.Repeat(string(rune('A'+n%26)), 1500), 1)
		}
		p.feedReads(b.Bytes())
		ch := p.Subscribe("late", 0)
		var out []byte
		for len(ch) > 0 {
			out = takeChunk(out, <-ch, p.subscriberFor("late"))
		}
		if b64.MatchString(screenOf(out)) {
			bad++
		}
		if n := len(p.gfx.spans); n > 64<<10/1500+1 {
			t.Fatalf("the scanner keeps %d spans for a 64 KiB ring of 1.5 KB frames", n)
		}
	}
	if bad > 0 {
		t.Fatalf("%d of 50 catch-ups printed image data as text", bad)
	}
}

// TestCatchUpSkipsTheRestOfACommand: a client that subscribes while a large
// graphics command is still arriving starts after it. The command is a frame
// (mpv's), or a transmission that is not one (an image with no id that has
// an alpha channel).
func TestCatchUpSkipsTheRestOfACommand(t *testing.T) {
	b64 := regexp.MustCompile(`[A-Za-z0-9+/]{30,}`)
	for _, keys := range []string{"", "a=T,f=32,s=100,v=100,C=1,q=2"} {
		t.Run(fmt.Sprintf("keys %q", keys), func(t *testing.T) {
			var b bytes.Buffer
			for n := range 8 {
				b.Write(mpvFrame(n, "\x1b[H", keys))
			}
			stream := b.Bytes()
			bad, trials := 0, 0
			for cut := 70 << 10; cut < 200<<10; cut += 997 {
				trials++
				p := queuePTY()
				p.feedReads(stream[:cut])
				ch := p.SubscribeFromSnapshot("late", int64(cut))
				p.feedReads(stream[cut : cut+(80<<10)])
				var out []byte
				for len(ch) > 0 {
					out = takeChunk(out, <-ch, p.subscriberFor("late"))
				}
				if b64.MatchString(screenOf(out)) {
					bad++
				}
			}
			if bad > 0 {
				t.Fatalf("%d of %d catch-ups printed image data as text", bad, trials)
			}
		})
	}
}

// screenOf is the text a client shows for out.
func screenOf(out []byte) string {
	s := replayClient(out)
	return s[:strings.Index(s, "\ncursor ")]
}

// TestKittyQueryIsNotGraphicsOutput: many programs send one kitty query when
// they start. A text flood after it is read like any text pane, never held.
// A flood after a frame is held, so the fixture does reach the hold.
func TestKittyQueryIsNotGraphicsOutput(t *testing.T) {
	for _, first := range []string{
		"\x1b_Gi=31,s=1,v=1,a=q,t=d,f=24;AAAA\x1b\\",
		"\x1b_Ga=T,f=32,s=1,v=1,i=1,C=1,q=2;AAAA\x1b\\",
	} {
		p := pacingPTY(t)
		_ = p.Subscribe("slow", 0)
		p.feedRing([]byte(first))
		line := bytes.Repeat([]byte("text text text\r\n"), 64)
		for range 4096 {
			p.holdForSlowSubscribers()
			p.feedRing(line)
		}
		query := strings.Contains(first, "a=q")
		if holds := p.holds.Load(); query != (holds == 0) {
			t.Fatalf("after %q a text flood was held %d times", first, holds)
		}
	}
}
