package tuie2e

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
