// Command kittylive walks the kitty graphics transmission media in a pane and
// prints PASS or FAIL for each, with the terminal's reply. It is the check a
// person runs to see that images work, and the guest the e2e tests drive.
//
// Usage: kittylive [-dir DIR] [-pause] [-refuse] [-only NAME,...]
//
// The steps, in order:
//
//	query        a=q, the probe kitten icat sends first
//	query-file   a=q for a file (t=f), which icat sends to pick file mode
//	direct-png   t=d PNG (f=100), chunked at 4 KiB
//	direct-rgba  t=d raw RGBA (f=32)
//	file         t=f, a regular file in DIR, which must be kept
//	file-offset  t=f with O= and S=: a PNG in the middle of a junk file
//	tempfile     t=t, a file in $TMPDIR named tty-graphics-protocol, which the
//	             terminal must delete after reading
//	shm          t=s, a POSIX shared memory object, which the terminal must
//	             unlink after reading (Linux only)
//
// With -refuse it runs the commands a terminal must refuse instead, each of
// which must get an error reply: a /proc path, a file of another user, a
// symlink in DIR that leads into /proc, and, with -deny FILE, a file the
// link's read rules keep from other machines.
//
// The first line it prints says whether the pane runs on a linked machine. The last line is
// "ALL n PASS" or "n FAIL".
package main

import (
	"bytes"
	"encoding/base64"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type step struct {
	name, desc string
	// run sends the command and returns what the screen should show and a
	// check run after the reply.
	run func() (expect string, after func() (string, bool))
	// refuse says the reply must be an error.
	refuse bool
}

var (
	dir    = flag.String("dir", "", "where t=f files are written (default: a new folder in $TMPDIR)")
	pause  = flag.Bool("pause", false, "wait for Enter after each step")
	refuse = flag.Bool("refuse", false, "run the commands a terminal must refuse")
	deny   = flag.String("deny", "", "with -refuse: a file the link's read rules deny")
	only   = flag.String("only", "", "run only these steps, comma separated")
)

func main() {
	flag.Parse()
	if *dir == "" {
		d, err := os.MkdirTemp("", "kittylive-")
		if err != nil {
			fail("cannot make a folder: %v", err)
		}
		*dir = d
		defer func() { _ = os.RemoveAll(d) }()
	}
	saved, _ := exec.Command("stty", "-g").CombinedOutput()
	stty("-icanon", "-echo", "min", "0", "time", "1")
	defer stty(strings.TrimSpace(string(saved)))
	replies := make(chan byte, 1<<16)
	go func() {
		buf := make([]byte, 256)
		for {
			n, err := os.Stdin.Read(buf)
			for _, c := range buf[:n] {
				replies <- c
			}
			if err != nil && n == 0 {
				time.Sleep(10 * time.Millisecond)
			}
		}
	}()

	where := "a local pane"
	if os.Getenv("TUIOS_PANE_HOSTED") == "1" {
		where = "a pane on a linked machine"
	}
	fmt.Printf("\x1b[1mkitty graphics check\x1b[0m in \x1b[1;36m%s\x1b[0m (%s)\n", where, runtime.GOOS)

	steps := mediaSteps()
	if *refuse {
		steps = refuseSteps()
	}
	want := map[string]bool{}
	for n := range strings.SplitSeq(*only, ",") {
		if n != "" {
			want[n] = true
		}
	}
	passed, failed := 0, 0
	for _, s := range steps {
		if len(want) > 0 && !want[s.name] {
			continue
		}
		fmt.Printf("\x1b[1m== %s\x1b[0m: %s\n", s.name, s.desc)
		expect, after := s.run()
		reply := readReply(replies, 3*time.Second)
		ok := strings.HasSuffix(reply, ";OK\x1b\\")
		if s.refuse {
			ok = reply != "" && !strings.Contains(reply, ";OK")
		}
		note := ""
		if after != nil {
			var good bool
			note, good = after()
			ok = ok && good
		}
		if expect != "" {
			fmt.Printf("\nexpect: %s%s\n", expect, note)
		} else if note != "" {
			fmt.Printf("%s\n", strings.TrimPrefix(note, "; "))
		}
		if ok {
			passed++
			fmt.Printf("\x1b[32mPASS\x1b[0m reply=%s\n", shown(reply))
		} else {
			failed++
			fmt.Printf("\x1b[31mFAIL\x1b[0m reply=%s\n", shown(reply))
		}
		if *pause {
			fmt.Print("-- check the screen, then press Enter --")
			waitEnter(replies)
			fmt.Println()
		}
	}
	if failed == 0 {
		fmt.Printf("\x1b[1;32mALL %d PASS\x1b[0m\n", passed)
	} else {
		fmt.Printf("\x1b[1;31m%d FAIL\x1b[0m, %d pass\n", failed, passed)
	}
}

func mediaSteps() []step {
	return []step{
		{name: "query", desc: "a=q support query, the same probe icat uses", run: func() (string, func() (string, bool)) {
			apc("a=q,i=31,s=1,v=1,t=d,f=24", []byte{0, 0, 0})
			return "", nil
		}},
		{name: "query-file", desc: "a=q for a file (t=f), how icat picks file mode", run: func() (string, func() (string, bool)) {
			p := filepath.Join(*dir, "probe.rgb")
			write(p, []byte{1, 2, 3})
			apc("a=q,i=32,s=1,v=1,t=f,f=24", []byte(p))
			return "", nil
		}},
		{name: "direct-png", desc: "t=d PNG (f=100), chunked at 4 KiB", run: func() (string, func() (string, bool)) {
			chunked("a=T,i=1,f=100,c=16,r=4", checker(color.RGBA{220, 50, 47, 255}))
			return "red and white checkerboard above", nil
		}},
		{name: "direct-rgba", desc: "t=d raw RGBA (f=32), chunked at 4 KiB", run: func() (string, func() (string, bool)) {
			w, h := 64, 32
			pix := bytes.Repeat([]byte{38, 139, 210, 255}, w*h)
			chunked(fmt.Sprintf("a=T,i=2,f=32,s=%d,v=%d,c=8,r=2", w, h), pix)
			return "solid blue block above", nil
		}},
		{name: "file", desc: "t=f: a regular file, read on the machine the pane runs on", run: func() (string, func() (string, bool)) {
			p := filepath.Join(*dir, "green.png")
			write(p, checker(color.RGBA{133, 153, 0, 255}))
			apc("a=T,i=3,f=100,t=f,c=16,r=4", []byte(p))
			return "green and white checkerboard above", func() (string, bool) {
				_, err := os.Stat(p)
				return fmt.Sprintf("; file kept: %v", err == nil), err == nil
			}
		}},
		{name: "file-offset", desc: "t=f with O= and S=: a PNG in the middle of a junk file", run: func() (string, func() (string, bool)) {
			img := solid(color.RGBA{203, 75, 22, 255})
			junk := bytes.Repeat([]byte("junk"), 1000)
			p := filepath.Join(*dir, "junk.bin")
			write(p, append(append(append([]byte{}, junk...), img...), junk...))
			apc(fmt.Sprintf("a=T,i=4,f=100,t=f,O=%d,S=%d,c=8,r=4", len(junk), len(img)), []byte(p))
			return "solid orange square above", nil
		}},
		{name: "tempfile", desc: "t=t: a temp file named tty-graphics-protocol; the terminal deletes it", run: func() (string, func() (string, bool)) {
			f, err := os.CreateTemp("", "kittylive-*-tty-graphics-protocol.png")
			if err != nil {
				fail("temp file: %v", err)
			}
			_, _ = f.Write(checker(color.RGBA{108, 113, 196, 255}))
			_ = f.Close()
			p := f.Name()
			apc("a=T,i=5,f=100,t=t,c=16,r=4", []byte(p))
			return "purple and white checkerboard above", func() (string, bool) {
				gone := waitGone(p)
				return fmt.Sprintf("; temp file deleted: %v", gone), gone
			}
		}},
		{name: "shm", desc: "t=s: a POSIX shared memory object; the terminal unlinks it", run: func() (string, func() (string, bool)) {
			if runtime.GOOS != "linux" {
				apc("a=q,i=6,s=1,v=1,t=d,f=24", []byte{0, 0, 0})
				return "", func() (string, bool) { return "; skipped: no /dev/shm here", true }
			}
			w, h := 64, 32
			name := fmt.Sprintf("kittylive-%d-tty-graphics-protocol", os.Getpid())
			write("/dev/shm/"+name, bytes.Repeat([]byte{42, 161, 152, 255}, w*h))
			apc(fmt.Sprintf("a=T,i=6,f=32,t=s,s=%d,v=%d,S=%d,c=8,r=2", w, h, w*h*4), []byte(name))
			return "solid teal block above", func() (string, bool) {
				gone := waitGone("/dev/shm/" + name)
				return fmt.Sprintf("; shared memory unlinked: %v", gone), gone
			}
		}},
	}
}

func refuseSteps() []step {
	steps := []step{
		{name: "proc", desc: "t=f /proc/self/status: nothing in /proc is read", refuse: true, run: func() (string, func() (string, bool)) {
			apc("a=T,i=41,f=100,t=f", []byte("/proc/self/status"))
			return "", nil
		}},
		{name: "not-owned", desc: "t=f a file of another user (/etc/passwd)", refuse: true, run: func() (string, func() (string, bool)) {
			apc("a=T,i=42,f=100,t=f", []byte("/etc/passwd"))
			return "", nil
		}},
		{name: "symlink-escape", desc: "t=f a symlink here that leads into /proc", refuse: true, run: func() (string, func() (string, bool)) {
			p := filepath.Join(*dir, "escape.png")
			_ = os.Remove(p)
			if err := os.Symlink("/proc/self/environ", p); err != nil {
				fail("symlink: %v", err)
			}
			apc("a=T,i=43,f=100,t=f", []byte(p))
			return "", nil
		}},
		{name: "temp-outside", desc: "t=t a file outside the temporary folders", refuse: true, run: func() (string, func() (string, bool)) {
			p := filepath.Join(*dir, "x-tty-graphics-protocol.png")
			write(p, checker(color.RGBA{0, 0, 0, 255}))
			apc("a=T,i=44,f=100,t=t", []byte(p))
			return "", nil
		}},
	}
	if *deny != "" {
		steps = append(steps, step{name: "link-deny", desc: "t=f a file the link's read rules deny", refuse: true, run: func() (string, func() (string, bool)) {
			apc("a=T,i=45,f=100,t=f", []byte(*deny))
			return "", nil
		}})
		steps = append(steps, step{name: "symlink-deny", desc: "t=f a symlink here that leads to that file", refuse: true, run: func() (string, func() (string, bool)) {
			p := filepath.Join(*dir, "deny-link.png")
			_ = os.Remove(p)
			if err := os.Symlink(*deny, p); err != nil {
				fail("symlink: %v", err)
			}
			apc("a=T,i=46,f=100,t=f", []byte(p))
			return "", nil
		}})
	}
	return steps
}

// apc sends one graphics command with payload base64 encoded.
func apc(keys string, payload []byte) {
	fmt.Printf("\x1b_G%s;%s\x1b\\", keys, base64.StdEncoding.EncodeToString(payload))
}

// chunked sends data as a t=d upload in 4096-character chunks.
func chunked(keys string, data []byte) {
	enc := base64.StdEncoding.EncodeToString(data)
	var b strings.Builder
	for i := 0; i < len(enc); i += 4096 {
		end := min(i+4096, len(enc))
		more := 0
		if end < len(enc) {
			more = 1
		}
		if i == 0 {
			fmt.Fprintf(&b, "\x1b_G%s,m=%d;%s\x1b\\", keys, more, enc[i:end])
		} else {
			fmt.Fprintf(&b, "\x1b_Gm=%d;%s\x1b\\", more, enc[i:end])
		}
	}
	fmt.Print(b.String())
}

// checker is a 160x80 PNG of c and white squares.
func checker(c color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 160, 80))
	for y := range 80 {
		for x := range 160 {
			if (x/20+y/20)%2 == 0 {
				img.Set(x, y, c)
			} else {
				img.Set(x, y, color.RGBA{255, 255, 255, 255})
			}
		}
	}
	return encode(img)
}

func solid(c color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	for y := range 100 {
		for x := range 100 {
			img.Set(x, y, c)
		}
	}
	return encode(img)
}

func encode(img image.Image) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		fail("png: %v", err)
	}
	return buf.Bytes()
}

func write(path string, data []byte) {
	if err := os.WriteFile(path, data, 0o600); err != nil {
		fail("write %s: %v", path, err)
	}
}

// waitGone waits up to two seconds for path to be removed.
func waitGone(path string) bool {
	for range 40 {
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = os.Remove(path)
	return false
}

// readReply reads one graphics reply, or nothing when none comes in time.
func readReply(in <-chan byte, wait time.Duration) string {
	var b []byte
	t := time.NewTimer(wait)
	defer t.Stop()
	for {
		select {
		case c := <-in:
			if len(b) == 0 && c != 0x1b {
				continue
			}
			b = append(b, c)
			if bytes.HasPrefix(b, []byte("\x1b_G")) && bytes.HasSuffix(b, []byte("\x1b\\")) {
				return string(b)
			}
			if len(b) >= 2 && b[1] != '_' {
				b = nil
			}
		case <-t.C:
			return string(b)
		}
	}
}

func waitEnter(in <-chan byte) {
	for c := range in {
		if c == '\r' || c == '\n' {
			return
		}
	}
}

// shown writes a reply the way Python's repr would.
func shown(s string) string {
	if s == "" {
		return "(none)"
	}
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "\x1b", `\x1b`)
	return "'" + s + "'"
}

func stty(args ...string) {
	cmd := exec.Command("stty", args...)
	cmd.Stdin = os.Stdin
	_ = cmd.Run()
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "kittylive: "+format+"\n", args...)
	os.Exit(1)
}
