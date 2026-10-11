package tuie2e

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The third review of link file access: what a linked machine can reach
// through the transfer verbs, how much it may write, and what it may see.
//
// build lets the hub relay and use files unless a test says otherwise, so the
// hub may call transfer-start and drop-files on build. Every call goes over
// the real link: the hub dials build through the ssh stand-in.

// relayFilesPolicy lets a linked machine relay and use the file verbs: the
// capabilities transfer-start and drop-files need.
const relayFilesPolicy = "[hosts.\"*\"]\nallow = [\"list\", \"mail\", \"open\", \"write\", \"respond\", \"files\"]\n"

// farTransfer waits for a copy on build, asked through c, to end.
func farTransfer(t *testing.T, c *fileVerbConn, id string, within time.Duration) transferRow {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		var out struct {
			Transfers []transferRow `json:"transfers"`
		}
		c.must("transfer-list", map[string]any{"id": id}, &out)
		if len(out.Transfers) != 1 {
			t.Fatalf("copy %s is not listed on build", id)
		}
		r := out.Transfers[0]
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

// TestALinkTransferStartKeepsToTheLinkPolicy is the third review's finding
// 1: transfer-start and drop-files read and write this machine's disk, so a
// linked machine that calls them is held to the rules its own file verbs
// keep. build keeps the default write root, the receive folder.
func TestALinkTransferStartKeepsToTheLinkPolicy(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	farHome := xdgDir(remote, "HOME")
	writeRemoteConfig(t, remote, relayFilesPolicy)
	if err := os.MkdirAll(filepath.Join(farHome, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	const key = "-----BEGIN OPENSSH PRIVATE KEY-----\nsecret\n"
	if err := os.WriteFile(filepath.Join(farHome, ".ssh", "id_ed25519"), []byte(key), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(farHome, "notes.txt"), []byte("ordinary\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const rc = "# the person's own bashrc\n"
	if err := os.WriteFile(filepath.Join(farHome, ".bashrc"), []byte(rc), 0o644); err != nil {
		t.Fatal(err)
	}
	hubWithFileHost(t, base, remote, writeFakeSSHTo(t, base, remote))

	far := dialHostVerbs(t, base)
	record := map[string]any{}
	refused := map[string]map[string]any{
		"a private key out to the receive folder": {
			"src": map[string]any{"path": "~/.ssh/id_ed25519"}, "dst": map[string]any{"path": "~/Downloads/tuios/key"}, "conflict": "replace",
		},
		"a file onto .bashrc": {
			"src": map[string]any{"path": "~/notes.txt"}, "dst": map[string]any{"path": "~/.bashrc"}, "conflict": "replace",
		},
		"a file outside the write roots": {
			"src": map[string]any{"path": "~/notes.txt"}, "dst": map[string]any{"path": "~/elsewhere.txt"}, "conflict": "replace",
		},
		"a move of a file outside the write roots": {
			"src": map[string]any{"path": "~/notes.txt"}, "dst": map[string]any{"path": "~/Downloads/tuios/moved.txt"}, "conflict": "replace", "move": true,
		},
	}
	for what, params := range refused {
		raw, err := far.call("transfer-start", params)
		record[what] = errString(err)
		if forbidden(err) {
			continue
		}
		t.Errorf("ASSERTION: transfer-start of %s over the link was not refused before a job started: %v", what, err)
		// A job that started must still end refused, with nothing written.
		var row transferRow
		_ = json.Unmarshal(raw, &row)
		if row.ID != "" {
			record[what] = farTransfer(t, far, row.ID, 30*time.Second)
		}
	}
	_, dropErr := far.call("drop-files", map[string]any{"paths": []string{"~/.ssh/id_ed25519"}})
	record["drop-files of a private key"] = errString(dropErr)
	if !forbidden(dropErr) {
		t.Errorf("ASSERTION: drop-files of a private key over the link was not refused: %v", dropErr)
	}
	if _, err := os.Stat(filepath.Join(farHome, "Downloads", "tuios", "key")); err == nil {
		t.Errorf("ASSERTION: the private key was copied into the receive folder")
	}
	if got, _ := os.ReadFile(filepath.Join(farHome, ".bashrc")); string(got) != rc {
		t.Errorf("ASSERTION: build's .bashrc changed: %q", got)
	}
	if _, err := os.Stat(filepath.Join(farHome, "elsewhere.txt")); err == nil {
		t.Errorf("ASSERTION: a copy landed outside the write roots")
	}
	if _, err := os.Stat(filepath.Join(farHome, "notes.txt")); err != nil {
		t.Errorf("ASSERTION: the move removed a file outside the write roots: %v", err)
	}

	// The positive half: a copy into the receive folder works.
	var ok transferRow
	far.must("transfer-start", map[string]any{
		"src": map[string]any{"path": "~/notes.txt"}, "dst": map[string]any{"path": "~/Downloads/tuios/notes.txt"}, "conflict": "replace",
	}, &ok)
	ok = farTransfer(t, far, ok.ID, 30*time.Second)
	record["a copy into the receive folder"] = ok
	if ok.State != "done" || !ok.Verified {
		t.Errorf("ASSERTION: a copy into the receive folder ended %s (%s): %s", ok.State, ok.Code, ok.Error)
	}
	saveTransferArtifact(t, "link-transfer-start", record)
}

// TestALinkJobKeepsItsPolicyAcrossARestart: a job a linked machine started
// is held to that machine's policy after the daemon restarts from its
// journal. build is killed while the copy runs, its files_roots is narrowed
// so the destination is outside them, and the daemon starts again. The
// resumed copy must end forbidden, with nothing in place.
func TestALinkJobKeepsItsPolicyAcrossARestart(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	farHome := xdgDir(remote, "HOME")
	writeRemoteConfig(t, remote, relayFilesPolicy)
	big := filepath.Join(farHome, "big.bin")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(2 << 30); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	hubWithFileHost(t, base, remote, writeFakeSSHTo(t, base, remote))

	pid := daemonPID(t, remote)
	far := dialHostVerbs(t, base)
	var row transferRow
	far.must("transfer-start", map[string]any{
		"src": map[string]any{"path": "~/big.bin"}, "dst": map[string]any{"path": "~/Downloads/tuios/big.bin"}, "conflict": "replace",
	}, &row)
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatalf("kill build's daemon: %v", err)
	}
	for i := 0; i < 100 && syscall.Kill(pid, 0) == nil; i++ {
		time.Sleep(50 * time.Millisecond)
	}
	journal := filepath.Join(xdgDir(remote, "XDG_STATE_HOME"), "tuios", "sessions", "transfers", row.ID+".json")
	entry, err := os.ReadFile(journal)
	if err != nil {
		t.Fatalf("the copy ended before build's daemon was killed, so there is nothing to resume: %v", err)
	}

	writeRemoteConfig(t, remote, relayFilesPolicy+"files_roots = [\"~/elsewhere\"]\n")
	if out, err := tuiosCLI(t, remote, "new", "again", "--detach"); err != nil {
		t.Fatalf("start build's daemon again: %v\n%s", err, out)
	}
	end := farTransfer(t, dialFileVerbs(t, remote), row.ID, 60*time.Second)
	saveTransferArtifact(t, "link-job-across-a-restart", map[string]any{"journal": string(entry), "after": end})
	if !strings.Contains(string(entry), `"link"`) {
		t.Errorf("ASSERTION: the journal does not say a linked machine started the copy: %s", entry)
	}
	if end.State != "failed" || end.Code != "forbidden" {
		t.Errorf("ASSERTION: the resumed copy ended %s (%s), want failed as forbidden: %s", end.State, end.Code, end.Error)
	}
	if _, err := os.Stat(filepath.Join(farHome, "Downloads", "tuios", "big.bin")); err == nil {
		t.Errorf("ASSERTION: the copy landed outside the roots in force after the restart")
	}
}

// writeStream opens a write stream on build through c and sends body. It
// returns the refusal, or the reply line after the bytes.
func writeStream(c *fileVerbConn, path, partID string, length int64, body []byte) (map[string]any, error) {
	params := map[string]any{"path": path, "mode": "write", "length": length}
	if partID != "" {
		params["part_id"] = partID
	}
	if _, err := c.call("open-file-stream", params); err != nil {
		return nil, err
	}
	// An empty body is not written at all. A zero-length Write is still a
	// write(2) on the socket, and with length 0 the far daemon sends its
	// closing line and closes as soon as the stream opens. The hub's relay
	// then closes this end, so the empty write raced the close and failed
	// with a broken pipe on a slow runner. The reply is in the buffer anyway.
	if len(body) > 0 {
		if _, err := c.conn.Write(body); err != nil {
			return nil, err
		}
	}
	line, err := c.br.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	var reply map[string]any
	err = json.Unmarshal(line, &reply)
	return reply, err
}

func code(err error) string {
	var vf *verbFailure
	if errors.As(err, &vf) {
		return vf.Code
	}
	return ""
}

// TestTheDropFolderTakesOnlyDropsWithinItsBudget is the third review's
// finding 2: the drop folder is often in memory (tmpfs), so a linked machine
// with list and files must not be able to fill it. It may write only inside
// a drop file-drop-dir made, and a write that would take the folder past
// 256 MiB is refused before a byte moves.
func TestTheDropFolderTakesOnlyDropsWithinItsBudget(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	writeRemoteConfig(t, remote, "[hosts.\"*\"]\nallow = [\"list\", \"files\"]\n")
	hubWithFileHost(t, base, remote, writeFakeSSHTo(t, base, remote))

	var drop struct {
		Dir string `json:"dir"`
	}
	dialHostVerbs(t, base).must("file-drop-dir", map[string]any{"bytes": 10}, &drop)
	top := filepath.Dir(drop.Dir)
	record := map[string]any{"drop": drop.Dir}

	_, topErr := writeStream(dialHostVerbs(t, base), filepath.Join(top, "filler.bin"), "", 5, []byte("12345"))
	record["write at the top"] = errString(topErr)
	if !forbidden(topErr) {
		t.Errorf("ASSERTION: a write at the top of the drop folder was not refused: %v", topErr)
	}
	_, mkErr := dialHostVerbs(t, base).call("file-mkdir", map[string]any{"path": filepath.Join(top, "mine")})
	record["mkdir at the top"] = errString(mkErr)
	if !forbidden(mkErr) {
		t.Errorf("ASSERTION: a folder at the top of the drop folder was not refused: %v", mkErr)
	}

	_, bigErr := writeStream(dialHostVerbs(t, base), filepath.Join(drop.Dir, "huge.bin"), "", 300<<20, nil)
	record["300 MiB into a drop"] = errString(bigErr)
	if code(bigErr) != "disk_full" {
		t.Errorf("ASSERTION: a 300 MiB write into a drop was not refused as disk_full: %v", bigErr)
	}
	entries, _ := os.ReadDir(drop.Dir)
	if len(entries) != 0 {
		t.Errorf("ASSERTION: the refused write left %d entries in the drop", len(entries))
	}
	topEntries, _ := os.ReadDir(top)
	for _, e := range topEntries {
		if !e.IsDir() {
			t.Errorf("ASSERTION: a file is at the top of the drop folder: %s", e.Name())
		}
	}

	// The positive half: a small file goes into the drop.
	reply, err := writeStream(dialHostVerbs(t, base), filepath.Join(drop.Dir, "small.txt"), "", 5, []byte("hello"))
	record["5 bytes into a drop"] = reply
	if err != nil || reply["error"] != nil {
		t.Errorf("ASSERTION: a small write into a drop failed: %v %v", err, reply)
	}
	saveTransferArtifact(t, "drop-folder-budget", record)
}

// TestALinkWriteLeavesFreeSpaceAndKeepsToItsCaps is the third review's
// finding 3: a write over a link must leave files_min_free on its disk, and
// one linked machine has at most 8 write streams and 64 part files open.
func TestALinkWriteLeavesFreeSpaceAndKeepsToItsCaps(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	farHome := xdgDir(remote, "HOME")
	writeRemoteConfig(t, remote, "[hosts.\"*\"]\nallow = [\"list\", \"files\"]\nfiles_roots = [\"~\"]\nfiles_min_free = \"1000000TiB\"\n")
	hubWithFileHost(t, base, remote, writeFakeSSHTo(t, base, remote))
	record := map[string]any{}

	_, fullErr := writeStream(dialHostVerbs(t, base), "~/x.bin", "", 5, []byte("12345"))
	record["write under a reserve the disk cannot keep"] = errString(fullErr)
	if code(fullErr) != "disk_full" || !strings.Contains(errString(fullErr), "files_min_free") {
		t.Errorf("ASSERTION: a write that leaves less than files_min_free was not refused as disk_full naming the setting: %v", fullErr)
	}
	if parts := partsIn(t, farHome); len(parts) != 0 {
		t.Errorf("ASSERTION: the refused write left a part: %v", parts)
	}

	// A reserve of 0 turns the check off, so the caps can be reached.
	writeRemoteConfig(t, remote, "[hosts.\"*\"]\nallow = [\"list\", \"files\"]\nfiles_roots = [\"~\"]\nfiles_min_free = \"0\"\n")
	if out, err := tuiosCLI(t, remote, "config", "apply"); err != nil {
		t.Fatalf("apply build's config: %v\n%s", err, out)
	}
	var held []*fileVerbConn
	var streamErr error
	opened := 0
	for i := range 9 {
		c := dialHostVerbs(t, base)
		_, err := c.call("open-file-stream", map[string]any{"path": "~/s.bin", "mode": "write", "length": 10, "part_id": "a" + string(rune('0'+i))})
		if err != nil {
			streamErr = err
			break
		}
		held = append(held, c)
		opened++
	}
	record["streams opened before the refusal"] = opened
	record["the refusal"] = errString(streamErr)
	if opened != 8 || code(streamErr) != "busy" {
		t.Errorf("ASSERTION: opened %d write streams, want 8 and then busy: %v", opened, streamErr)
	}
	for _, c := range held {
		_ = c.conn.Close()
	}
	time.Sleep(200 * time.Millisecond)

	// Each closed stream left an empty part. Empty writes add parts until
	// the cap.
	parts := len(partsIn(t, farHome))
	var partErr error
	for i := 0; i < 80 && partErr == nil; i++ {
		id := "b" + strings.Repeat("0", 2-len(itoa(i))) + itoa(i)
		if _, err := writeStream(dialHostVerbs(t, base), "~/p.bin", id, 0, nil); err != nil {
			partErr = err
			break
		}
		parts++
	}
	record["parts before the refusal"] = parts
	record["the part refusal"] = errString(partErr)
	if parts != 64 || code(partErr) != "busy" {
		t.Errorf("ASSERTION: %d parts were made, want 64 and then busy: %v", parts, partErr)
	}
	saveTransferArtifact(t, "link-write-limits", record)
}

// TestALinkReadsOnlyItsReadRoots is the third review's finding 5: with
// files_read_roots set, a linked machine reads only there and in the folders
// it may write, and the refusal names the setting.
func TestALinkReadsOnlyItsReadRoots(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	farHome := xdgDir(remote, "HOME")
	writeRemoteConfig(t, remote, "[hosts.\"*\"]\nallow = [\"list\", \"files\"]\nfiles_read_roots = [\"~/share\"]\n")
	for _, f := range []string{"share/a.txt", "notes.txt", "Downloads/tuios/r.txt"} {
		p := filepath.Join(farHome, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("text\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	hubWithFileHost(t, base, remote, writeFakeSSHTo(t, base, remote))

	far := dialHostVerbs(t, base)
	record := map[string]any{}
	for _, path := range []string{"~/share/a.txt", "~/Downloads/tuios/r.txt"} {
		_, err := far.call("file-read", map[string]any{"path": path})
		record["read "+path] = errString(err)
		if err != nil {
			t.Errorf("ASSERTION: a read of %s, inside the roots, failed: %v", path, err)
		}
	}
	for verb, params := range map[string]map[string]any{
		"file-read": {"path": "~/notes.txt"},
		"file-stat": {"path": "~/notes.txt"},
		"file-list": {"dir": "~"},
		"file-hash": {"path": "~/notes.txt"},
	} {
		_, err := far.call(verb, params)
		record[verb+" outside"] = errString(err)
		if !forbidden(err) || !strings.Contains(errString(err), "files_read_roots") {
			t.Errorf("ASSERTION: %s outside files_read_roots was not refused naming the setting: %v", verb, err)
		}
	}
	saveTransferArtifact(t, "link-read-roots", record)
}

// TestALinkCannotLookInsideDeniedFolders is the third review's finding 6 and
// the history entries of finding 5: file-stat and file-list keep to the read
// deny list too, so a linked machine cannot list the names in ~/.ssh or the
// open files and folders of every process in /proc, and a shell history is
// not read.
func TestALinkCannotLookInsideDeniedFolders(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	farHome := xdgDir(remote, "HOME")
	if err := os.MkdirAll(filepath.Join(farHome, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		".ssh/id_ed25519": "secret\n",
		".bash_history":   "export TOKEN=secret\n",
		"notes.txt":       "ordinary\n",
	} {
		if err := os.WriteFile(filepath.Join(farHome, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	hubWithFileHost(t, base, remote, writeFakeSSHTo(t, base, remote))

	far := dialHostVerbs(t, base)
	record := map[string]any{}
	refused := []struct {
		verb   string
		params map[string]any
	}{
		{"file-list", map[string]any{"dir": "~/.ssh"}},
		{"file-stat", map[string]any{"path": "~/.ssh/id_ed25519"}},
		{"file-read", map[string]any{"path": "~/.bash_history"}},
	}
	if runtime.GOOS == "linux" {
		refused = append(refused,
			struct {
				verb   string
				params map[string]any
			}{"file-list", map[string]any{"dir": "/proc/self/fd"}},
			struct {
				verb   string
				params map[string]any
			}{"file-stat", map[string]any{"path": "/proc/self/cwd"}},
		)
	}
	for _, r := range refused {
		_, err := far.call(r.verb, r.params)
		record[r.verb+" "+toJSON(r.params)] = errString(err)
		if !forbidden(err) {
			t.Errorf("ASSERTION: %s %v over the link was not refused: %v", r.verb, r.params, err)
		}
	}
	// The positive half.
	for verb, params := range map[string]map[string]any{
		"file-stat": {"path": "~/notes.txt"},
		"file-list": {"dir": "~"},
	} {
		if _, err := far.call(verb, params); err != nil {
			t.Errorf("ASSERTION: %s %v failed: %v", verb, params, err)
		}
	}
	saveTransferArtifact(t, "link-stat-and-list", record)
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
