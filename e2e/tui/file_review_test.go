package tuie2e

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/png"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// What the review of the file verbs found, each as the attack or the load
// that shows it.
//
// The far machine is not trusted to name where its files land here. A real
// tuios daemon never sends a name with "..", so the tests that need a lying
// machine put a fake daemon on build's socket: the hub reaches it through the
// real ssh stand-in, stdio-proxy and mux, and it answers the file verbs the
// way a daemon an attacker controls could.

// fakeFar is a daemon socket on the far machine that answers verbs from a
// table. Each handler returns the result, or an error code, and may take the
// connection over after the reply (open-file-stream).
type fakeFar struct {
	t    *testing.T
	ln   net.Listener
	mu   sync.Mutex
	seen []string
}

type fakeReply struct {
	result   any
	code     string
	takeover func(c net.Conn)
}

func startFakeFar(t *testing.T, remote string, handle func(verb string, params map[string]any) fakeReply) *fakeFar {
	t.Helper()
	dir := filepath.Join(xdgDir(remote, "XDG_RUNTIME_DIR"), "tuios")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", filepath.Join(dir, "tuios.sock"))
	if err != nil {
		t.Fatalf("listen as build's daemon: %v", err)
	}
	f := &fakeFar{t: t, ln: ln}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(c, handle)
		}
	}()
	return f
}

func (f *fakeFar) serve(c net.Conn, handle func(string, map[string]any) fakeReply) {
	defer func() { _ = c.Close() }()
	br := bufio.NewReader(c)
	for {
		line, err := br.ReadBytes('\n')
		if err != nil {
			return
		}
		var req struct {
			ID     json.RawMessage `json:"id"`
			Verb   string          `json:"verb"`
			Params map[string]any  `json:"params"`
		}
		if json.Unmarshal(line, &req) != nil {
			return
		}
		f.mu.Lock()
		f.seen = append(f.seen, req.Verb)
		f.mu.Unlock()
		var r fakeReply
		if req.Verb == "hello" {
			r.result = map[string]any{"type": "hello", "protocol": 1, "min_protocol": 1, "daemon_version": "dev", "sessions": 0}
		} else {
			r = handle(req.Verb, req.Params)
		}
		resp := map[string]any{"id": req.ID}
		if r.code != "" {
			resp["error"] = map[string]any{"code": r.code, "message": "the fake far daemon does not do " + req.Verb}
		} else {
			resp["result"] = r.result
		}
		out, _ := json.Marshal(resp)
		if _, err := c.Write(append(out, '\n')); err != nil {
			return
		}
		if r.takeover != nil {
			r.takeover(c)
			return
		}
	}
}

func shaHex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// hubWithFakeFar starts the hub with build reached through the ssh stand-in,
// where the fake daemon answers.
func hubWithFakeFar(t *testing.T, base, remote string) {
	t.Helper()
	ssh := writeFakeSSHTo(t, base, remote)
	writeOneHostConfig(t, base, tuiosBin)
	killDaemon(t, base)
	if out, err := tuiosCLIEnv(t, base, []string{"TUIOS_SSH=" + ssh}, "new", "home", "--detach"); err != nil {
		t.Fatalf("start the hub daemon: %v\n%s", err, out)
	}
	waitForHostListing(t, base, func(s string) bool {
		return strings.Contains(s, "build") && strings.Contains(s, "up")
	}, "the hub never reported build up")
}

// waitTransferEnd polls a transfer until it is done, failed or cancelled.
func waitTransferEnd(t *testing.T, base, id string, within time.Duration) transferRow {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		r := transferNow(t, base, id)
		switch r.State {
		case "done", "failed", "cancelled":
			return r
		}
		if time.Now().After(deadline) {
			return r
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestAFolderFromAHostCannotLandOutsideItsDestination copies a folder from a
// far machine that lies: its walk names files "../../escaped.txt" and
// "sub/../../../escaped-too.txt". Every byte must stay inside the folder the
// person chose, and the copy must fail and say why.
func TestAFolderFromAHostCannotLandOutsideItsDestination(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	payload := []byte("written by the far machine\n")
	evil := []string{"../../escaped.txt", "sub/../../../escaped-too.txt"}
	startFakeFar(t, remote, func(verb string, p map[string]any) fakeReply {
		path, _ := p["path"].(string)
		switch verb {
		case "file-stat":
			if path == "/far/folder" && p["part"] == nil {
				return fakeReply{result: map[string]any{"path": path, "exists": true, "size": 4096, "info": map[string]any{"name": "folder", "path": path, "kind": "dir", "perm": 0o755}}}
			}
			return fakeReply{result: map[string]any{"path": path, "exists": false, "size": 0}}
		case "file-walk":
			entries := []map[string]any{{"rel": "sub", "dir": true}}
			for _, rel := range evil {
				entries = append(entries, map[string]any{"rel": rel, "size": len(payload), "perm": 0o644})
			}
			return fakeReply{result: map[string]any{"path": path, "entries": entries, "bytes": len(payload) * len(evil)}}
		case "file-hash":
			return fakeReply{result: map[string]any{"path": path, "sha256": shaHex(payload), "bytes": len(payload)}}
		case "open-file-stream":
			return fakeReply{
				result:   map[string]any{"path": path, "mode": "read", "size": len(payload), "offset": 0},
				takeover: func(c net.Conn) { _, _ = c.Write(payload) },
			}
		}
		return fakeReply{code: "unknown_verb"}
	})
	hubWithFakeFar(t, base, remote)

	dest := filepath.Join(base, "downloads", "deep", "folder")
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	var row transferRow
	dialVerbs(t, base).must("transfer-start", map[string]any{
		"src": map[string]any{"host": "build", "path": "/far/folder"},
		"dst": map[string]any{"path": dest},
	}, &row)
	row = waitTransferEnd(t, base, row.ID, 30*time.Second)

	var escaped []string
	_ = filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasPrefix(d.Name(), "escaped") {
			escaped = append(escaped, p)
		}
		return nil
	})
	saveTransferArtifact(t, "folder-traversal", map[string]any{"row": row, "escaped": escaped})
	for _, p := range escaped {
		if !strings.HasPrefix(p, dest+string(filepath.Separator)) {
			t.Errorf("ASSERTION: the far machine wrote %s, outside %s", p, dest)
		}
	}
	if row.State != "failed" || !strings.Contains(row.Error, "outside") {
		t.Fatalf("ASSERTION: the copy of a folder with names that leave it ended %s (%s %s), want failed with a reason", row.State, row.Code, row.Error)
	}
}

// callWithin is one verb call that must answer within d.
func (c *verbConn) callWithin(d time.Duration, verb string, params any) (json.RawMessage, error) {
	c.id++
	line, _ := json.Marshal(map[string]any{"id": c.id, "verb": verb, "params": params})
	_ = c.conn.SetDeadline(time.Now().Add(d))
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
		return nil, err
	}
	if resp.Error != nil {
		return resp.Result, resp.Error
	}
	return resp.Result, nil
}

// daemonPeakKB is the most memory the daemon of base has held, from /proc.
func daemonPeakKB(t *testing.T, base string) int64 {
	t.Helper()
	var hello struct {
		PID int `json:"pid"`
	}
	dialVerbs(t, base).must("hello", nil, &hello)
	b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(hello.PID), "status"))
	if err != nil {
		t.Skipf("no /proc here: %v", err)
	}
	for line := range strings.SplitSeq(string(b), "\n") {
		if rest, ok := strings.CutPrefix(line, "VmHWM:"); ok {
			n, _ := strconv.ParseInt(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest), "kB")), 10, 64)
			return n
		}
	}
	t.Fatalf("no VmHWM in the daemon's status")
	return 0
}

// writePNGBomb writes a PNG of w x h transparent pixels. The rows are all
// zero, so the file is small and the picture is not.
func writePNGBomb(t *testing.T, path string, w, h int) {
	t.Helper()
	var idat bytes.Buffer
	zw, _ := zlib.NewWriterLevel(&idat, zlib.BestCompression)
	row := make([]byte, 1+w*4)
	for range h {
		_, _ = zw.Write(row)
	}
	_ = zw.Close()
	chunk := func(out *bytes.Buffer, typ string, data []byte) {
		_ = binary.Write(out, binary.BigEndian, uint32(len(data)))
		out.WriteString(typ)
		out.Write(data)
		_ = binary.Write(out, binary.BigEndian, crc32.ChecksumIEEE(append([]byte(typ), data...)))
	}
	var out bytes.Buffer
	out.WriteString("\x89PNG\r\n\x1a\n")
	var hdr bytes.Buffer
	_ = binary.Write(&hdr, binary.BigEndian, uint32(w))
	_ = binary.Write(&hdr, binary.BigEndian, uint32(h))
	hdr.Write([]byte{8, 6, 0, 0, 0})
	chunk(&out, "IHDR", hdr.Bytes())
	chunk(&out, "IDAT", idat.Bytes())
	chunk(&out, "IEND", nil)
	if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestFileVerbsOnAPipeADeviceAndAHugePicture asks the file verbs about
// three things a folder can hold that are not an ordinary file: a named
// pipe, which blocks whoever opens it to read, /dev/zero, which never ends,
// and a 400 KB PNG that decodes to 400 MB. Every verb must answer at once,
// and four previews of the picture together must not take the daemon's
// memory past 512 MB. An ordinary picture still previews.
func TestFileVerbsOnAPipeADeviceAndAHugePicture(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "home", "--detach"); err != nil {
		t.Fatalf("start the daemon: %v\n%s", err, out)
	}
	pipe := filepath.Join(base, "pipe")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Skipf("no named pipes here: %v", err)
	}
	type probe struct {
		Verb   string         `json:"verb"`
		Params map[string]any `json:"params"`
		Ms     int64          `json:"ms"`
		Answer string         `json:"answer"`
	}
	var probes []probe
	for _, p := range []probe{
		{Verb: "file-preview", Params: map[string]any{"path": pipe}},
		{Verb: "file-read", Params: map[string]any{"path": pipe}},
		{Verb: "file-hash", Params: map[string]any{"path": pipe}},
		{Verb: "open-file-stream", Params: map[string]any{"path": pipe}},
		{Verb: "file-hash", Params: map[string]any{"path": "/dev/zero"}},
		{Verb: "file-read", Params: map[string]any{"path": "/dev/zero"}},
	} {
		c := dialVerbs(t, base)
		start := time.Now()
		raw, err := c.callWithin(5*time.Second, p.Verb, p.Params)
		p.Ms = time.Since(start).Milliseconds()
		p.Answer = string(raw)
		if err != nil {
			p.Answer = err.Error()
		}
		probes = append(probes, p)
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			t.Errorf("ASSERTION: %s on %v did not answer in 5 s", p.Verb, p.Params["path"])
		}
	}

	bomb := filepath.Join(base, "huge.png")
	writePNGBomb(t, bomb, 10000, 10000)
	var wg sync.WaitGroup
	answers := make([]string, 4)
	for i := range answers {
		wg.Go(func() {
			c := dialVerbs(t, base)
			raw, err := c.callWithin(60*time.Second, "file-preview", map[string]any{"path": bomb})
			var out struct {
				Kind string `json:"kind"`
				Note string `json:"note"`
			}
			_ = json.Unmarshal(raw, &out)
			answers[i] = fmt.Sprintf("%s %s %v", out.Kind, out.Note, err)
		})
	}
	wg.Wait()
	peak := daemonPeakKB(t, base)

	small := filepath.Join(base, "small.png")
	img := image.NewRGBA(image.Rect(0, 0, 2000, 1000))
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	_ = os.WriteFile(small, buf.Bytes(), 0o644)
	var pic struct {
		Kind       string `json:"kind"`
		ImageWidth int    `json:"image_width"`
	}
	dialVerbs(t, base).must("file-preview", map[string]any{"path": small, "max_px": 500}, &pic)

	saveTransferArtifact(t, "file-verbs-not-a-file", map[string]any{"probes": probes, "bomb_previews": answers, "daemon_peak_kb": peak, "small": pic})
	t.Logf("daemon peak %d kB; bomb previews %q", peak, answers)
	if peak > 512<<10 {
		t.Errorf("ASSERTION: four previews of a 10000 x 10000 PNG took the daemon to %d MB", peak>>10)
	}
	if pic.Kind != "image" || pic.ImageWidth != 500 {
		t.Errorf("ASSERTION: an ordinary picture no longer previews: %+v", pic)
	}
}

// TestATransferStoppedWhileQueuedStaysStopped fills the three running slots
// with copies over a slow link, then cancels a fourth copy and pauses a
// fifth while they wait for a slot. When the slots free, neither may run:
// the cancelled one stays cancelled and the paused one paused, and neither
// file arrives.
func TestATransferStoppedWhileQueuedStaysStopped(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	hubWithFileHost(t, base, remote, writeSlowFakeSSH(t, base, remote, 1<<20, 256<<10))

	c := dialVerbs(t, base)
	start := func(name string, size int) transferRow {
		src := filepath.Join(remote, name)
		randomFile(t, src, size)
		var row transferRow
		c.must("transfer-start", map[string]any{
			"src": map[string]any{"host": "build", "path": src},
			"dst": map[string]any{"path": filepath.Join(base, name)},
		}, &row)
		return row
	}
	var running []transferRow
	for i := range 3 {
		running = append(running, start(fmt.Sprintf("busy%d.bin", i), 16<<20))
	}
	cancelled := start("cancelled.bin", 64<<10)
	paused := start("paused.bin", 64<<10)
	if r := transferNow(t, base, cancelled.ID); r.State != "queued" {
		t.Fatalf("the fourth copy is %s, want queued behind three", r.State)
	}
	c.must("transfer-cancel", map[string]any{"id": cancelled.ID}, nil)
	c.must("transfer-pause", map[string]any{"id": paused.ID}, nil)
	for _, r := range running {
		c.must("transfer-cancel", map[string]any{"id": r.ID}, nil)
	}
	time.Sleep(4 * time.Second)

	rc, rp := transferNow(t, base, cancelled.ID), transferNow(t, base, paused.ID)
	saveTransferArtifact(t, "stopped-while-queued", map[string]any{"cancelled": rc, "paused": rp})
	if rc.State != "cancelled" {
		t.Errorf("ASSERTION: the copy cancelled while queued is %s", rc.State)
	}
	if rp.State != "paused" {
		t.Errorf("ASSERTION: the copy paused while queued is %s", rp.State)
	}
	for _, name := range []string{"cancelled.bin", "paused.bin"} {
		if _, err := os.Stat(filepath.Join(base, name)); err == nil {
			t.Errorf("ASSERTION: %s arrived although its copy was stopped while queued", name)
		}
	}
	// The positive half: the paused copy goes on when it is resumed.
	c.must("transfer-resume", map[string]any{"id": paused.ID}, nil)
	if r := waitTransferEnd(t, base, paused.ID, 30*time.Second); r.State != "done" || !r.Verified {
		t.Errorf("ASSERTION: the paused copy, resumed, ended %s (verified %v): %s", r.State, r.Verified, r.Error)
	}
}
