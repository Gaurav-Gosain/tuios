package tuie2e

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// Files, transfers, drops and previews between two machines.
//
// The far machine is the second daemon of host_attach_test.go, reached only
// through the hub daemon's link: the ssh stand-in switches the XDG
// directories, so every byte crosses the real subprocess transport, framing,
// proxy and mux. For the tests about speed the stand-in also puts a slow link
// in the way: this test binary, run as a throttle, passes the far side's
// output on at a fixed rate through a buffer as deep as ssh's channel window.
//
// What would pass a weaker test and fail these: a transfer that starts over
// after a drop (resumed_from stays empty, or the bytes copied twice), a copy
// that is not checked (a changed byte is put in place), a link that sends a
// copy's bytes ahead of a pane's (the round trips during the copy grow to the
// depth of the buffer), and a drop that lands on this machine instead of the
// pane's.

const (
	throttleMarker = "tuios-e2e-throttle"
	throttleEnv    = "TUIOS_E2E_THROTTLE"
)

// runThrottleIfAsked turns this test binary into the far end of a slow link.
// It runs the far command given after "--" with this process's stdin, and
// passes the command's output on at rate bytes a second through a buffer of
// the given depth. The command is its child, so killing this process closes
// the link's pipe the way a dropped connection does. TestMain calls it first.
func runThrottleIfAsked() {
	spec := os.Getenv(throttleEnv)
	if spec == "" {
		return
	}
	var rate, depth int
	if _, err := fmt.Sscanf(spec, "%d:%d", &rate, &depth); err != nil || rate <= 0 || depth <= 0 {
		os.Exit(2)
	}
	cmdline := ""
	for i, a := range os.Args {
		if a == "--" && i+1 < len(os.Args) {
			cmdline = strings.Join(os.Args[i+1:], " ")
			break
		}
	}
	cmd := exec.Command("/bin/sh", "-c", cmdline)
	cmd.Env = slicesWithout(os.Environ(), throttleEnv)
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr
	far, err := cmd.StdoutPipe()
	if err != nil || cmd.Start() != nil {
		os.Exit(255)
	}
	var mu sync.Mutex
	cond := sync.NewCond(&mu)
	var buf []byte
	eof := false
	go func() {
		chunk := make([]byte, 64<<10)
		for {
			n, err := far.Read(chunk)
			mu.Lock()
			for len(buf) >= depth {
				cond.Wait()
			}
			buf = append(buf, chunk[:n]...)
			if err != nil {
				eof = true
			}
			cond.Broadcast()
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	const tick = 2 * time.Millisecond
	per := max(rate*int(tick)/int(time.Second), 1)
	next := time.Now()
	for {
		mu.Lock()
		for len(buf) == 0 && !eof {
			cond.Wait()
		}
		if len(buf) == 0 && eof {
			mu.Unlock()
			_ = cmd.Wait()
			os.Exit(0)
		}
		k := min(per, len(buf))
		out := append([]byte(nil), buf[:k]...)
		buf = buf[k:]
		cond.Broadcast()
		mu.Unlock()
		if _, err := os.Stdout.Write(out); err != nil {
			_ = cmd.Process.Kill()
			os.Exit(0)
		}
		next = next.Add(tick)
		if d := time.Until(next); d > 0 {
			time.Sleep(d)
		} else {
			next = time.Now()
		}
	}
}

func slicesWithout(env []string, key string) []string {
	out := env[:0:0]
	for _, kv := range env {
		if !strings.HasPrefix(kv, key+"=") {
			out = append(out, kv)
		}
	}
	return out
}

// writeSlowFakeSSH is writeFakeSSHTo with the far side's output passed on at
// rate bytes a second through depth bytes of buffer. The throttle's command
// line names base, so a test can cut this link and no other.
func writeSlowFakeSSH(t *testing.T, dir, remoteBase string, rate, depth int) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("find this test binary: %v", err)
	}
	path := filepath.Join(dir, "fake-ssh-slow")
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("while [ $# -gt 0 ]; do\n  case \"$1\" in\n    -o) shift 2 ;;\n    -T) shift ;;\n    -t) shift ;;\n    --) shift; break ;;\n    *) break ;;\n  esac\ndone\n")
	b.WriteString("shift\n")
	for _, key := range xdgKeys {
		b.WriteString("export " + key + "=" + xdgDir(remoteBase, key) + "\n")
	}
	fmt.Fprintf(&b, "%s=%d:%d exec %s %s %s -- \"$*\"\n", throttleEnv, rate, depth, self, throttleMarker, dir)
	if err := os.WriteFile(path, []byte(b.String()), 0o700); err != nil {
		t.Fatalf("write the slow ssh stand-in: %v", err)
	}
	return path
}

// cutSlowLink kills the throttles of the links whose stand-in lives in dir:
// the far side's output stops dead, as when a cable is pulled.
func cutSlowLink(t *testing.T, dir string) int {
	t.Helper()
	out, err := exec.Command("ps", "-A", "-ww", "-o", "pid=,args=").Output()
	if err != nil {
		t.Fatalf("list processes: %v", err)
	}
	killed := 0
	for line := range strings.SplitSeq(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, throttleMarker+" "+dir) {
			continue
		}
		pid, err := strconv.Atoi(strings.Fields(line)[0])
		if err != nil {
			continue
		}
		if syscall.Kill(pid, syscall.SIGKILL) == nil {
			killed++
		}
	}
	return killed
}

// hubWithHost starts the hub daemon with one host, build, reached with ssh,
// and waits for the link to be up.
func hubWithFileHost(t *testing.T, base, remote, ssh string) []string {
	t.Helper()
	// The far daemon runs, as on a machine where someone uses tuios. The
	// proxy never starts one.
	if out, err := tuiosCLI(t, remote, "new", "far", "--detach"); err != nil {
		t.Fatalf("start build's daemon: %v\n%s", err, out)
	}
	writeOneHostConfig(t, base, tuiosBin)
	env := []string{"TUIOS_SSH=" + ssh}
	killDaemon(t, base)
	if out, err := tuiosCLIEnv(t, base, env, "new", "home", "--detach"); err != nil {
		t.Fatalf("start the hub daemon: %v\n%s", err, out)
	}
	waitForHostListing(t, base, func(s string) bool {
		return strings.Contains(s, "build") && strings.Contains(s, "up")
	}, "the hub never reported build up")
	return env
}

// verbConn is one JSON verb connection to a daemon.
type verbConn struct {
	t    *testing.T
	conn net.Conn
	br   *bufio.Reader
	id   int
}

func hubSocket(base string) string {
	return filepath.Join(xdgDir(base, "XDG_RUNTIME_DIR"), "tuios", "tuios.sock")
}

func dialVerbs(t *testing.T, base string) *verbConn {
	t.Helper()
	conn, err := net.Dial("unix", hubSocket(base))
	if err != nil {
		t.Fatalf("dial the daemon: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return &verbConn{t: t, conn: conn, br: bufio.NewReaderSize(conn, 1<<20)}
}

// dialHostVerbs is a verb connection to build's daemon, through the hub.
func dialHostVerbs(t *testing.T, base string) *verbConn {
	t.Helper()
	c := dialVerbs(t, base)
	if _, err := c.call("open-host-connection", map[string]any{"host": "build"}); err != nil {
		t.Fatalf("open a connection to build: %v", err)
	}
	return c
}

type verbFailure struct{ Code, Message string }

func (e *verbFailure) Error() string { return e.Code + ": " + e.Message }

func (c *verbConn) call(verb string, params any) (json.RawMessage, error) {
	c.id++
	line, _ := json.Marshal(map[string]any{"id": c.id, "verb": verb, "params": params})
	_ = c.conn.SetDeadline(time.Now().Add(60 * time.Second))
	if _, err := c.conn.Write(append(line, '\n')); err != nil {
		return nil, err
	}
	reply, err := c.br.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *verbFailure    `json:"error"`
	}
	if err := json.Unmarshal(reply, &resp); err != nil {
		return nil, fmt.Errorf("decode %q: %w", reply, err)
	}
	if resp.Error != nil {
		return nil, resp.Error
	}
	return resp.Result, nil
}

func (c *verbConn) must(verb string, params any, out any) {
	c.t.Helper()
	raw, err := c.call(verb, params)
	if err != nil {
		c.t.Fatalf("%s: %v", verb, err)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			c.t.Fatalf("decode %s: %v\n%s", verb, err, raw)
		}
	}
}

type transferRow struct {
	ID          string `json:"id"`
	State       string `json:"state"`
	Size        int64  `json:"size"`
	Done        int64  `json:"done"`
	Rate        float64
	ResumedFrom int64  `json:"resumed_from"`
	Resumes     int    `json:"resumes"`
	Verified    bool   `json:"verified"`
	SHA256      string `json:"sha256"`
	Final       string `json:"final"`
	Error       string `json:"error"`
	Code        string `json:"code"`
}

func transferNow(t *testing.T, base, id string) transferRow {
	t.Helper()
	var out struct {
		Transfers []transferRow `json:"transfers"`
	}
	c := dialVerbs(t, base)
	defer func() { _ = c.conn.Close() }()
	c.must("transfer-list", map[string]any{"id": id}, &out)
	if len(out.Transfers) != 1 {
		t.Fatalf("transfer %s is not listed", id)
	}
	return out.Transfers[0]
}

// randomFile writes size random bytes to path and returns their sha256.
func randomFile(t *testing.T, path string, size int) string {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	h := sha256.New()
	if _, err := io.CopyN(io.MultiWriter(f, h), rand.Reader, int64(size)); err != nil {
		t.Fatalf("fill %s: %v", path, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close %s: %v", path, err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func fileSHA(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// saveArtifact writes a test's record where TUIOS_E2E_FRAMES points.
func saveTransferArtifact(t *testing.T, name string, v any) {
	t.Helper()
	dir := os.Getenv("TUIOS_E2E_FRAMES")
	if dir == "" {
		return
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, name+".json"), b, 0o644); err != nil {
		t.Logf("could not save %s: %v", name, err)
	}
}

// TestATransferFromAHostResumesAfterTheLinkDrops copies 96 MiB from build to
// this machine over a link of 24 MB/s, and cuts the link a third of the way
// in. The job must wait for build, go on from the bytes it already has, and
// put a file in place whose sha256 is the original's, even though a byte of
// the part was changed while the link was down.
func TestATransferFromAHostResumesAfterTheLinkDrops(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	const size = 96 << 20
	src := filepath.Join(remote, "big.bin")
	want := randomFile(t, src, size)

	ssh := writeSlowFakeSSH(t, base, remote, 24<<20, 1<<20)
	hubWithFileHost(t, base, remote, ssh)

	dst := filepath.Join(base, "copy of big.bin")
	var row transferRow
	dialVerbs(t, base).must("transfer-start", map[string]any{
		"src": map[string]any{"host": "build", "path": src},
		"dst": map[string]any{"path": dst},
	}, &row)

	type point struct {
		Ms    int64  `json:"ms"`
		State string `json:"state"`
		Done  int64  `json:"done"`
	}
	var timeline []point
	start := time.Now()
	note := func(r transferRow) {
		timeline = append(timeline, point{time.Since(start).Milliseconds(), r.State, r.Done})
	}

	// Cut a third of the way in.
	deadline := time.Now().Add(60 * time.Second)
	for {
		row = transferNow(t, base, row.ID)
		note(row)
		if row.Done >= size/3 {
			break
		}
		if row.State == "failed" || time.Now().After(deadline) {
			t.Fatalf("the copy never got a third of the way: %+v", row)
		}
		time.Sleep(50 * time.Millisecond)
	}
	atCut := row.Done
	if n := cutSlowLink(t, base); n == 0 {
		t.Fatalf("no link to cut")
	}
	t.Logf("cut the link at %d of %d bytes", atCut, size)
	// A byte of the part changes while the link is down, far from the end the
	// resume compares. Only the whole-file check can catch it, and it must:
	// the copy may not put that file in place.
	part := filepath.Join(base, ".copy of big.bin.tuios-part")
	if f, err := os.OpenFile(part, os.O_WRONLY, 0); err != nil {
		t.Fatalf("open the part: %v", err)
	} else {
		if _, err := f.WriteAt([]byte{0xA5, 0x5A, 0xA5, 0x5A}, 4096); err != nil {
			t.Fatalf("change the part: %v", err)
		}
		_ = f.Close()
	}

	sawWaiting := false
	deadline = time.Now().Add(90 * time.Second)
	for {
		row = transferNow(t, base, row.ID)
		note(row)
		if row.State == "waiting" {
			sawWaiting = true
		}
		if row.State == "done" || row.State == "failed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: the copy did not finish after the link came back: %+v", row)
		}
		time.Sleep(100 * time.Millisecond)
	}
	saveTransferArtifact(t, "transfer-resume", map[string]any{"cut_at": atCut, "size": size, "final": row, "timeline": timeline})
	t.Logf("final row: %+v", row)

	if row.State != "done" {
		t.Fatalf("ASSERTION: the copy ended %s: %s", row.State, row.Error)
	}
	if !sawWaiting {
		t.Fatalf("ASSERTION: the copy never said it was waiting for build while the link was down")
	}
	if row.Resumes < 1 || row.ResumedFrom < atCut/2 {
		t.Fatalf("ASSERTION: the copy started over after the drop: resumed_from %d with %d bytes copied at the cut", row.ResumedFrom, atCut)
	}
	if !row.Verified || row.SHA256 != want {
		t.Fatalf("ASSERTION: the copy was not checked against the original: verified %v, sha256 %s, want %s", row.Verified, row.SHA256, want)
	}
	if got := fileSHA(t, dst); got != want {
		t.Fatalf("ASSERTION: the file in place is not the original: %s, want %s", got, want)
	}
	if _, err := os.Stat(filepath.Join(base, ".copy of big.bin.tuios-part")); !os.IsNotExist(err) {
		t.Fatalf("ASSERTION: the part file was left behind: %v", err)
	}
}

// TestATransferLeavesTypingOnItsMachineFast measures a pane's round trip on
// build while a copy from build fills the link. The link carries 8 MB/s with
// 2 MiB of buffer in the middle, the size of ssh's channel window. Every
// round trip during the copy must stay under 150 ms. A copy that is not held
// behind the pane fills the buffer, and every round trip waits for 2 MiB at
// 8 MB/s, a quarter of a second, plus a megabyte frame.
func TestATransferLeavesTypingOnItsMachineFast(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	const size = 48 << 20
	src := filepath.Join(remote, "big.bin")
	randomFile(t, src, size)
	ssh := writeSlowFakeSSH(t, base, remote, 8<<20, 2<<20)
	hubWithFileHost(t, base, remote, ssh)

	// The pane: a connection to build's daemon that answers one small call
	// at a time, which is the shape of a key and its echo.
	pane := dialHostVerbs(t, base)
	roundTrip := func() time.Duration {
		start := time.Now()
		pane.must("file-stat", map[string]any{"path": "/"}, nil)
		return time.Since(start)
	}
	var idle []time.Duration
	for range 10 {
		idle = append(idle, roundTrip())
		time.Sleep(20 * time.Millisecond)
	}

	var row transferRow
	dialVerbs(t, base).must("transfer-start", map[string]any{
		"src": map[string]any{"host": "build", "path": src},
		"dst": map[string]any{"path": filepath.Join(base, "big.bin")},
	}, &row)
	deadline := time.Now().Add(30 * time.Second)
	for row = transferNow(t, base, row.ID); row.Done < size/8; row = transferNow(t, base, row.ID) {
		if row.State == "failed" || time.Now().After(deadline) {
			t.Fatalf("the copy did not start: %+v", row)
		}
		time.Sleep(50 * time.Millisecond)
	}

	var busy []time.Duration
	for range 30 {
		busy = append(busy, roundTrip())
		time.Sleep(50 * time.Millisecond)
	}
	during := transferNow(t, base, row.ID)
	stats := func(v []time.Duration) map[string]float64 {
		s := append([]time.Duration(nil), v...)
		sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
		ms := func(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }
		return map[string]float64{"p50_ms": ms(s[len(s)/2]), "p90_ms": ms(s[len(s)*9/10]), "max_ms": ms(s[len(s)-1])}
	}
	report := map[string]any{"idle": stats(idle), "during_copy": stats(busy), "copy_rate_bytes_per_s": during.Rate, "copy_done": during.Done}
	saveTransferArtifact(t, "transfer-typing-latency", report)
	t.Logf("round trips on build: %v", report)
	if during.State != "running" && during.State != "verifying" && during.State != "done" {
		t.Fatalf("the copy was not running while the round trips were measured: %+v", during)
	}
	worst := stats(busy)["p90_ms"]
	if worst > 150 {
		t.Fatalf("ASSERTION: a pane's round trip on build was %.0f ms (p90) during the copy, want under 150 ms", worst)
	}
}

// TestDropFilesOnAPaneOnAHost drops two files from this machine on a pane on
// build. They must land on build, in a private folder, with the paths in the
// answer pointing at them.
func TestDropFilesOnAPaneOnAHost(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	hubWithFileHost(t, base, remote, writeFakeSSHTo(t, base, remote))

	a := filepath.Join(base, "notes.txt")
	if err := os.WriteFile(a, []byte("dropped from the desktop\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := filepath.Join(base, "photo one.bin")
	wantB := randomFile(t, b, 3<<20)

	var out struct {
		Paths     []string      `json:"paths"`
		Dir       string        `json:"dir"`
		Transfers []transferRow `json:"transfers"`
	}
	dialVerbs(t, base).must("drop-files", map[string]any{"host": "build", "paths": []string{a, b}}, &out)
	if len(out.Paths) != 2 || len(out.Transfers) != 2 {
		t.Fatalf("ASSERTION: the drop answered %+v", out)
	}
	farRuntime := xdgDir(remote, "XDG_RUNTIME_DIR")
	if !strings.HasPrefix(out.Dir, farRuntime) {
		t.Fatalf("ASSERTION: the drop folder %s is not in build's runtime folder %s", out.Dir, farRuntime)
	}
	for _, tr := range out.Transfers {
		deadline := time.Now().Add(30 * time.Second)
		for r := transferNow(t, base, tr.ID); r.State != "done"; r = transferNow(t, base, tr.ID) {
			if r.State == "failed" || time.Now().After(deadline) {
				t.Fatalf("ASSERTION: a dropped file did not reach build: %+v", r)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	got, err := os.ReadFile(out.Paths[0])
	if err != nil || string(got) != "dropped from the desktop\n" {
		t.Fatalf("ASSERTION: %s on build holds %q (%v)", out.Paths[0], got, err)
	}
	if fileSHA(t, out.Paths[1]) != wantB {
		t.Fatalf("ASSERTION: %s on build is not the dropped file", out.Paths[1])
	}
	if fi, err := os.Stat(filepath.Dir(out.Dir)); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("ASSERTION: the drop folder is not private: %v %v", fi.Mode(), err)
	}
	if fi, err := os.Stat(out.Paths[1]); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("ASSERTION: a dropped file is not owner only: %v %v", fi.Mode(), err)
	}
	saveTransferArtifact(t, "drop-files", out)
}

// TestFilesAndPreviewsOnAHost lists a folder on build and previews an image
// and a table there, through the hub, as the explorer does.
func TestFilesAndPreviewsOnAHost(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	hubWithFileHost(t, base, remote, writeFakeSSHTo(t, base, remote))

	dir := filepath.Join(remote, "work")
	for _, d := range []string{"src", "docs"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"file10.txt", "file2.txt", "data.csv"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("id,name\n1,ada\n2,linus\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	img := image.NewRGBA(image.Rect(0, 0, 3000, 2000))
	for y := range 2000 {
		for x := range 3000 {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wide.png"), pngBuf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	far := dialHostVerbs(t, base)
	var list struct {
		Dir     string `json:"dir"`
		Entries []struct {
			Name string `json:"name"`
			Kind string `json:"kind"`
			Size int64  `json:"size"`
			Mode string `json:"mode"`
		} `json:"entries"`
	}
	far.must("file-list", map[string]any{"dir": dir}, &list)
	var names []string
	for _, e := range list.Entries {
		names = append(names, e.Name)
	}
	want := []string{"docs", "src", "data.csv", "file2.txt", "file10.txt", "wide.png"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("ASSERTION: build's listing is %v, want folders first in natural order %v", names, want)
	}
	if list.Entries[0].Kind != "dir" || list.Entries[2].Size != 22 || !strings.HasPrefix(list.Entries[2].Mode, "-rw-") {
		t.Fatalf("ASSERTION: the listing lacks kinds, sizes or modes: %+v", list.Entries)
	}

	var pic struct {
		Kind        string `json:"kind"`
		Width       int    `json:"width"`
		ImageWidth  int    `json:"image_width"`
		ImageHeight int    `json:"image_height"`
		Image       string `json:"image"`
	}
	far.must("file-preview", map[string]any{"path": filepath.Join(dir, "wide.png"), "max_px": 1200}, &pic)
	if pic.Kind != "image" || pic.Width != 3000 || pic.ImageWidth != 1200 || pic.ImageHeight != 800 {
		t.Fatalf("ASSERTION: the image preview is %s %dpx scaled to %dx%d, want image 3000px scaled to 1200x800", pic.Kind, pic.Width, pic.ImageWidth, pic.ImageHeight)
	}
	raw, err := base64.StdEncoding.DecodeString(pic.Image)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(raw))
	if err != nil || cfg.Width != 1200 {
		t.Fatalf("ASSERTION: the preview is not a 1200 px PNG: %+v %v", cfg, err)
	}
	if len(raw) >= pngBuf.Len() {
		t.Fatalf("ASSERTION: the preview (%d bytes) is not smaller than the file (%d bytes)", len(raw), pngBuf.Len())
	}

	var text struct {
		Kind string `json:"kind"`
		Text string `json:"text"`
	}
	far.must("file-preview", map[string]any{"path": filepath.Join(dir, "data.csv")}, &text)
	if text.Kind != "text" || !strings.Contains(text.Text, "2,linus") {
		t.Fatalf("ASSERTION: the table preview is %+v", text)
	}
}

// TestFileBytesKeepToTheLinkPolicy: a far machine that lets this one only
// list sees a listing go through and a read of a file's bytes refused, and a
// copy from it fails with the reason instead of waiting.
func TestFileBytesKeepToTheLinkPolicy(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	writeRemoteConfig(t, remote, "[hosts.\"*\"]\nallow = [\"list\"]\n")
	hubWithFileHost(t, base, remote, writeFakeSSHTo(t, base, remote))
	secret := filepath.Join(remote, "secret.txt")
	if err := os.WriteFile(secret, []byte("not for the hub"), 0o600); err != nil {
		t.Fatal(err)
	}

	far := dialHostVerbs(t, base)
	far.must("file-list", map[string]any{"dir": remote}, nil)
	if _, err := far.call("file-read", map[string]any{"path": secret}); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("ASSERTION: a list-only link read a file's bytes: %v", err)
	}

	var row transferRow
	dialVerbs(t, base).must("transfer-start", map[string]any{
		"src": map[string]any{"host": "build", "path": secret},
		"dst": map[string]any{"path": filepath.Join(base, "secret.txt")},
	}, &row)
	deadline := time.Now().Add(20 * time.Second)
	for row = transferNow(t, base, row.ID); row.State != "failed"; row = transferNow(t, base, row.ID) {
		if row.State == "done" || time.Now().After(deadline) {
			t.Fatalf("ASSERTION: a copy through a list-only link did not fail: %+v", row)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if row.Code != "forbidden" {
		t.Fatalf("ASSERTION: the copy failed with %q (%s), want forbidden", row.Code, row.Error)
	}
	if _, err := os.Stat(filepath.Join(base, "secret.txt")); !os.IsNotExist(err) {
		t.Fatalf("ASSERTION: the file arrived anyway")
	}
}
