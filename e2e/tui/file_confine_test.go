package tuie2e

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// The review of 217e663d: what a linked machine can still reach through a
// hard link, a rename that races a read, open writes that the free space does
// not show yet, a copy that outlives its capability, and the directory
// listings outside the file verbs.
//
// Nothing here touches a real file of the person who runs the suite. The
// "protected" file is a scratch file in the far daemon's own temporary home
// folder, holding protectedMarker, and the "key" is a dummy file named like a
// key, holding dummyKeyMarker. Each test asserts the refusal and that the
// marker file is unchanged or was not returned.

const (
	protectedMarker = "ORIGINAL\n"
	dummyKeyMarker  = "DUMMY-KEY-MARKER\n"
)

// listFilesPolicy lets a linked machine list and use the file verbs, with the
// default write root, the receive folder.
const listFilesPolicy = "[hosts.\"*\"]\nallow = [\"list\", \"files\"]\n"

// relayNoFilesPolicy is relayFilesPolicy with files taken away: the machine
// may still relay, and may no longer copy here.
const relayNoFilesPolicy = "[hosts.\"*\"]\nallow = [\"list\", \"mail\", \"open\", \"write\", \"respond\"]\n"

// writeFarFile writes body to path, with its folders.
func writeFarFile(t *testing.T, path, body string, perm os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), perm); err != nil {
		t.Fatal(err)
	}
}

// dummyKey writes the dummy key at ~/.ssh/id_ed25519 in home and returns its
// path.
func dummyKey(t *testing.T, home string) string {
	t.Helper()
	key := filepath.Join(home, ".ssh", "id_ed25519")
	writeFarFile(t, key, dummyKeyMarker, 0o600)
	return key
}

// readContent reads path on build through c and returns its text.
func readContent(c *fileVerbConn, path string) (string, error) {
	raw, err := c.call("file-read", map[string]any{"path": path})
	if err != nil {
		return "", err
	}
	var out struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	b, err := base64.StdEncoding.DecodeString(out.Content)
	return string(b), err
}

// streamCode is the code of a write stream's refusal: from the call, or
// from the reply line after the bytes.
func streamCode(reply map[string]any, err error) string {
	if err != nil {
		return code(err)
	}
	if c, ok := reply["code"].(string); ok {
		return c
	}
	return ""
}

// TestALinkCannotWriteThroughAHardLinkedPart is the review's finding 1. A
// part file in the receive folder that is a hard link to a file elsewhere,
// such as one an archive put there, must not be written: its truncate and
// write would land in the other file. A linked machine must not rename a file
// onto a part name or off one either, so it cannot make such a part itself.
func TestALinkCannotWriteThroughAHardLinkedPart(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	farHome := xdgDir(remote, "HOME")
	writeRemoteConfig(t, remote, listFilesPolicy)
	recv := filepath.Join(farHome, "Downloads", "tuios")
	protected := filepath.Join(farHome, "protected.txt")
	writeFarFile(t, protected, protectedMarker, 0o644)
	if err := os.MkdirAll(recv, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(protected, filepath.Join(recv, ".out.tuios-part-abc")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(protected, filepath.Join(recv, "rclink")); err != nil {
		t.Fatal(err)
	}
	writeFarFile(t, filepath.Join(recv, "plain.txt"), "plain\n", 0o644)
	hubWithFileHost(t, base, remote, writeFakeSSHTo(t, base, remote))
	record := map[string]any{}
	payload := []byte("PAYLOAD-FROM-THE-LINK\n")

	reply, err := writeStream(dialHostVerbs(t, base), "~/Downloads/tuios/out", "abc", int64(len(payload)), payload)
	record["write into the hard-linked part"] = map[string]any{"reply": reply, "error": errString(err)}
	if got := streamCode(reply, err); got != "forbidden" {
		t.Errorf("ASSERTION: a write into a hard-linked part was not refused as forbidden: %v %v", reply, err)
	}

	far := dialHostVerbs(t, base)
	for what, params := range map[string]map[string]any{
		"a hard link onto a part name":  {"from": "~/Downloads/tuios/rclink", "to": "~/Downloads/tuios/.out2.tuios-part-abd"},
		"a plain file onto a part name": {"from": "~/Downloads/tuios/plain.txt", "to": "~/Downloads/tuios/.plain.tuios-part"},
		"a part off its part name":      {"from": "~/Downloads/tuios/.out.tuios-part-abc", "to": "~/Downloads/tuios/taken.txt"},
	} {
		_, err := far.call("file-rename", params)
		record["rename of "+what] = errString(err)
		if !forbidden(err) {
			t.Errorf("ASSERTION: the rename of %s over the link was not refused: %v", what, err)
		}
	}

	// The positive half: the repro's second step, a stream to the part name
	// the rename aimed at, makes a new part and leaves the protected file.
	reply, err = writeStream(dialHostVerbs(t, base), "~/Downloads/tuios/out2", "abd", int64(len(payload)), payload)
	record["write into a new part"] = map[string]any{"reply": reply, "error": errString(err)}
	if err != nil || reply["error"] != nil {
		t.Errorf("ASSERTION: a write into a new part failed: %v %v", reply, err)
	}
	if got, _ := os.ReadFile(filepath.Join(recv, ".out2.tuios-part-abd")); string(got) != string(payload) {
		t.Errorf("ASSERTION: the new part holds %q, want the payload", got)
	}

	got, err := os.ReadFile(protected)
	record["protected file after"] = string(got)
	if err != nil || string(got) != protectedMarker {
		t.Errorf("ASSERTION: the protected file changed through a hard link: %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(recv, "out")); err == nil {
		t.Errorf("ASSERTION: the refused write put a file in place")
	}
	for _, name := range []string{"rclink", "plain.txt", ".out.tuios-part-abc"} {
		if _, err := os.Lstat(filepath.Join(recv, name)); err != nil {
			t.Errorf("ASSERTION: %s is gone after a refused rename: %v", name, err)
		}
	}
	saveTransferArtifact(t, "link-hard-linked-part", record)
}

// TestALinkReadCannotRaceARename is the review's finding 2. The receive
// folder holds a folder whose k is a symlink to ~/.ssh, as an extracted
// archive can, and a folder whose k holds a harmless file of the same name.
// While the two are swapped under one name as fast as this process can
// rename them, a linked machine reads that name again and again. A check of
// the path before the open alone races the swap. The dummy key must never be
// returned.
func TestALinkReadCannotRaceARename(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	farHome := xdgDir(remote, "HOME")
	writeRemoteConfig(t, remote, listFilesPolicy)
	key := dummyKey(t, farHome)
	recv := filepath.Join(farHome, "Downloads", "tuios")
	const benign = "benign\n"
	writeFarFile(t, filepath.Join(recv, "good", "k", "id_ed25519"), benign, 0o644)
	if err := os.MkdirAll(filepath.Join(recv, "evil"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Dir(key), filepath.Join(recv, "evil", "k")); err != nil {
		t.Fatal(err)
	}
	hubWithFileHost(t, base, remote, writeFakeSSHTo(t, base, remote))
	far := dialHostVerbs(t, base)
	record := map[string]any{}

	// Without a race the symlink is followed before the check and refused.
	_, direct := readContent(far, "~/Downloads/tuios/evil/k/id_ed25519")
	record["read through the symlink"] = errString(direct)
	if !forbidden(direct) {
		t.Errorf("ASSERTION: a read through the symlink to ~/.ssh was not refused: %v", direct)
	}

	var stop atomic.Bool
	var swaps atomic.Int64
	var wg sync.WaitGroup
	wg.Go(func() {
		mv := func(a, b string) {
			if os.Rename(filepath.Join(recv, a), filepath.Join(recv, b)) == nil {
				swaps.Add(1)
			}
		}
		for !stop.Load() {
			mv("good", "x")
			mv("x", "good")
			mv("evil", "x")
			mv("x", "evil")
		}
	})
	const maxReads = 6000
	deadline := time.Now().Add(60 * time.Second)
	reads, benignReads, refused, other := 0, 0, 0, 0
	leaked := ""
	for reads < maxReads && time.Now().Before(deadline) {
		got, err := readContent(far, "~/Downloads/tuios/x/k/id_ed25519")
		reads++
		switch {
		case err == nil && strings.Contains(got, "DUMMY-KEY-MARKER"):
			leaked = got
		case err == nil && got == benign:
			benignReads++
		case forbidden(err):
			refused++
		default:
			other++
		}
		if leaked != "" {
			break
		}
	}
	stop.Store(true)
	wg.Wait()
	record["reads"], record["benign"], record["refused"], record["other"] = reads, benignReads, refused, other
	record["swaps"], record["leaked"] = swaps.Load(), leaked != ""
	t.Logf("reads=%d benign=%d refused=%d other=%d swaps=%d", reads, benignReads, refused, other, swaps.Load())
	if leaked != "" {
		t.Errorf("ASSERTION: a link read the dummy key through a rename race after %d reads", reads)
	}
	if benignReads == 0 || refused == 0 {
		t.Errorf("ASSERTION: the reads did not see both folders under the name (benign %d, refused %d), so the race did not run", benignReads, refused)
	}
	if got, _ := os.ReadFile(key); string(got) != dummyKeyMarker {
		t.Errorf("ASSERTION: the dummy key changed: %q", got)
	}
	saveTransferArtifact(t, "link-read-rename-race", record)
}

// TestOpenLinkWritesCountAgainstTheFreeSpaceReserve is the review's finding
// 3. The free space a disk reports does not count the writes that are open
// on it, so two streams that each fit the reserve alone must not both be
// taken when together they do not. Nothing is written: the claim is made when
// a stream opens.
func TestOpenLinkWritesCountAgainstTheFreeSpaceReserve(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	farHome := xdgDir(remote, "HOME")
	root := filepath.Join(farHome, "inbox")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	var st syscall.Statfs_t
	if err := syscall.Statfs(root, &st); err != nil {
		t.Fatal(err)
	}
	free := int64(st.Bavail) * int64(st.Bsize) //nolint:unconvert // the field types differ by system
	// Each stream claims 768 MiB against a reserve 1 GiB under the free
	// space now. The first fits while the disk loses less than 256 MiB in the
	// meantime, and the second does not fit unless the disk gains 512 MiB.
	const each = 768 << 20
	if free < 4<<30 {
		t.Skipf("the disk has %d MiB free, and the test needs 4 GiB", free>>20)
	}
	reserve := free - 1<<30
	writeRemoteConfig(t, remote, fmt.Sprintf("[hosts.\"*\"]\nallow = [\"list\", \"files\"]\nfiles_roots = [%q]\nfiles_min_free = \"%d\"\n", root, reserve))
	hubWithFileHost(t, base, remote, writeFakeSSHTo(t, base, remote))
	record := map[string]any{"free": free, "reserve": reserve, "each": each}

	first := dialHostVerbs(t, base)
	_, err := first.call("open-file-stream", map[string]any{"path": filepath.Join(root, "a.bin"), "mode": "write", "length": each})
	record["first stream"] = errString(err)
	if err != nil {
		t.Fatalf("the first stream, which fits the reserve, was refused: %v", err)
	}
	second := dialHostVerbs(t, base)
	_, err = second.call("open-file-stream", map[string]any{"path": filepath.Join(root, "b.bin"), "mode": "write", "length": each})
	record["second stream"] = errString(err)
	if code(err) != "disk_full" {
		t.Errorf("ASSERTION: a second open stream that takes the disk under files_min_free was not refused as disk_full: %v", err)
	}
	for _, p := range partsIn(t, root) {
		if strings.Contains(filepath.Base(p), ".b.bin") {
			t.Errorf("ASSERTION: the refused stream left a part: %s", p)
		}
	}
	_ = first.conn.Close()
	_ = second.conn.Close()

	// The positive half: once the first stream ends, its claim ends with it.
	time.Sleep(300 * time.Millisecond)
	third := dialHostVerbs(t, base)
	_, err = third.call("open-file-stream", map[string]any{"path": filepath.Join(root, "c.bin"), "mode": "write", "length": each})
	record["stream after the first closed"] = errString(err)
	if err != nil {
		t.Errorf("ASSERTION: a stream after the first one closed was refused: %v", err)
	}
	_ = third.conn.Close()
	saveTransferArtifact(t, "link-free-space-claims", record)
}

// startFarBigCopy starts a copy of 2 GiB on build over the link, into the
// receive folder, and returns its row.
func startFarBigCopy(t *testing.T, far *fileVerbConn, farHome string) transferRow {
	t.Helper()
	f, err := os.Create(filepath.Join(farHome, "big.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(2 << 30); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	var row transferRow
	far.must("transfer-start", map[string]any{
		"src": map[string]any{"path": "~/big.bin"}, "dst": map[string]any{"path": "~/Downloads/tuios/big.bin"}, "conflict": "replace",
	}, &row)
	return row
}

// TestALinkCopyStopsWhenItsFilesCapabilityGoes is the review's finding 4. A
// copy a linked machine started on build runs for that machine, so it must
// stop once build no longer lets the machine copy: after a restart that
// resumes it from the journal, and after a policy reload while it waits.
// The machine keeps relay both times; only files is taken away.
func TestALinkCopyStopsWhenItsFilesCapabilityGoes(t *testing.T) {
	t.Run("on resume after a restart", func(t *testing.T) {
		base := t.TempDir()
		remote := remoteMachine(t)
		farHome := xdgDir(remote, "HOME")
		writeRemoteConfig(t, remote, relayFilesPolicy)
		hubWithFileHost(t, base, remote, writeFakeSSHTo(t, base, remote))
		pid := daemonPID(t, remote)
		row := startFarBigCopy(t, dialHostVerbs(t, base), farHome)
		if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
			t.Fatalf("kill build's daemon: %v", err)
		}
		for i := 0; i < 100 && syscall.Kill(pid, 0) == nil; i++ {
			time.Sleep(50 * time.Millisecond)
		}
		journal := filepath.Join(xdgDir(remote, "XDG_STATE_HOME"), "tuios", "sessions", "transfers", row.ID+".json")
		if _, err := os.Stat(journal); err != nil {
			t.Fatalf("the copy ended before build's daemon was killed, so there is nothing to resume: %v", err)
		}
		writeRemoteConfig(t, remote, relayNoFilesPolicy)
		if out, err := tuiosCLI(t, remote, "new", "again", "--detach"); err != nil {
			t.Fatalf("start build's daemon again: %v\n%s", err, out)
		}
		end := farTransfer(t, dialFileVerbs(t, remote), row.ID, 60*time.Second)
		saveTransferArtifact(t, "link-copy-files-revoked-restart", end)
		if end.State != "failed" || end.Code != "forbidden" {
			t.Errorf("ASSERTION: the resumed copy ended %s (%s), want failed as forbidden: %s", end.State, end.Code, end.Error)
		}
		if _, err := os.Stat(filepath.Join(farHome, "Downloads", "tuios", "big.bin")); err == nil {
			t.Errorf("ASSERTION: the copy was put in place after its files capability was taken away")
		}
	})

	t.Run("after a policy reload", func(t *testing.T) {
		base := t.TempDir()
		remote := remoteMachine(t)
		farHome := xdgDir(remote, "HOME")
		writeRemoteConfig(t, remote, relayFilesPolicy)
		hubWithFileHost(t, base, remote, writeFakeSSHTo(t, base, remote))
		far := dialHostVerbs(t, base)
		row := startFarBigCopy(t, far, farHome)
		far.must("transfer-pause", map[string]any{"id": row.ID}, nil)
		// The pause stops the running attempt first, so the state follows.
		state := ""
		for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			if state = farTransfer(t, dialFileVerbs(t, remote), row.ID, 0).State; state != "running" {
				break
			}
		}
		if state != "paused" {
			t.Fatalf("the copy is %s after the pause, want paused", state)
		}
		writeRemoteConfig(t, remote, relayNoFilesPolicy)
		if out, err := tuiosCLI(t, remote, "config", "apply"); err != nil {
			t.Fatalf("apply build's config: %v\n%s", err, out)
		}
		end := farTransfer(t, dialFileVerbs(t, remote), row.ID, 15*time.Second)
		saveTransferArtifact(t, "link-copy-files-revoked-reload", end)
		if end.State != "failed" || end.Code != "forbidden" {
			t.Errorf("ASSERTION: a paused copy ended %s (%s) after the reload took files away, want failed as forbidden: %s", end.State, end.Code, end.Error)
		}
		if _, err := os.Stat(filepath.Join(farHome, "Downloads", "tuios", "big.bin")); err == nil {
			t.Errorf("ASSERTION: the copy was put in place after its files capability was taken away")
		}
	})
}

// readDirPayload is MsgReadDir's payload, as gob writes it by field name.
type readDirPayload struct{ Dir string }

// dirListing is the reply to MsgReadDir, as gob reads it by field name.
type dirListing struct {
	Dir     string
	Entries []struct {
		Name  string
		IsDir bool
	}
	Err string
}

// msgReadDir and msgDirListing are the binary message types of a listing
// (internal/session/protocol.go); msgError is the binary error.
const (
	msgReadDir    = 56
	msgDirListing = 57
	msgError      = 28
)

// binaryReadDir sends MsgReadDir for dir on c, a connection to build through
// the hub, and returns the answer.
func binaryReadDir(t *testing.T, c *fileVerbConn, dir string) dirListing {
	t.Helper()
	var payload bytes.Buffer
	if err := gob.NewEncoder(&payload).Encode(readDirPayload{Dir: dir}); err != nil {
		t.Fatal(err)
	}
	frame := make([]byte, 6, 6+payload.Len())
	binary.BigEndian.PutUint32(frame[:4], uint32(2+payload.Len()))
	frame[4], frame[5] = msgReadDir, 0
	frame = append(frame, payload.Bytes()...)
	_ = c.conn.SetDeadline(time.Now().Add(30 * time.Second))
	if _, err := c.conn.Write(frame); err != nil {
		t.Fatalf("send MsgReadDir: %v", err)
	}
	for {
		var n uint32
		if err := binary.Read(c.br, binary.BigEndian, &n); err != nil {
			t.Fatalf("read the answer to MsgReadDir: %v", err)
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(c.br, body); err != nil {
			t.Fatalf("read the answer to MsgReadDir: %v", err)
		}
		switch body[0] {
		case msgDirListing:
			var out dirListing
			if err := gob.NewDecoder(bytes.NewReader(body[2:])).Decode(&out); err != nil {
				t.Fatalf("decode the listing: %v", err)
			}
			return out
		case msgError:
			var e struct{ Message string }
			_ = gob.NewDecoder(bytes.NewReader(body[2:])).Decode(&e)
			return dirListing{Dir: dir, Err: "error: " + e.Message}
		}
	}
}

// TestALinkListsNoDeniedFolderOutsideTheFileVerbs is the review's finding 5:
// read-dir, wait-dir and the binary MsgReadDir list folders too, so over a
// link they keep to the read rules file-list keeps to. A machine that may
// list panes may not list ~/.ssh or /proc this way.
func TestALinkListsNoDeniedFolderOutsideTheFileVerbs(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	farHome := xdgDir(remote, "HOME")
	writeRemoteConfig(t, remote, listFilesPolicy)
	key := dummyKey(t, farHome)
	writeFarFile(t, filepath.Join(farHome, "notes.txt"), "ordinary\n", 0o644)
	hubWithFileHost(t, base, remote, writeFakeSSHTo(t, base, remote))
	sshDir := filepath.Dir(key)
	record := map[string]any{}

	denied := []string{sshDir}
	if runtime.GOOS == "linux" {
		denied = append(denied, "/proc/self/fd")
	}
	for _, dir := range denied {
		raw, err := dialHostVerbs(t, base).call("read-dir", map[string]any{"dir": dir})
		record["read-dir "+dir] = errString(err)
		if !forbidden(err) {
			t.Errorf("ASSERTION: read-dir %s over the link was not refused: %v", dir, err)
		}
		if strings.Contains(string(raw), "id_ed25519") {
			t.Errorf("ASSERTION: read-dir %s over the link listed the dummy key: %s", dir, raw)
		}

		_, err = dialHostVerbs(t, base).callWithin(15*time.Second, "wait-dir", map[string]any{"dir": dir, "timeout": 2000})
		record["wait-dir "+dir] = errString(err)
		if !forbidden(err) {
			t.Errorf("ASSERTION: wait-dir %s over the link was not refused: %v", dir, err)
		}

		got := binaryReadDir(t, dialHostVerbs(t, base), dir)
		record["MsgReadDir "+dir] = got
		if got.Err == "" || len(got.Entries) != 0 {
			t.Errorf("ASSERTION: MsgReadDir %s over the link was not refused: %+v", dir, got)
		}
	}

	// The positive half: each way lists the home folder.
	var listing struct {
		Entries []struct {
			Name string `json:"name"`
		} `json:"entries"`
	}
	dialHostVerbs(t, base).must("read-dir", map[string]any{"dir": farHome}, &listing)
	if !strings.Contains(toJSON(listing), "notes.txt") {
		t.Errorf("ASSERTION: read-dir of the home folder does not list notes.txt: %+v", listing)
	}
	if got := binaryReadDir(t, dialHostVerbs(t, base), farHome); got.Err != "" || !strings.Contains(toJSON(got), "notes.txt") {
		t.Errorf("ASSERTION: MsgReadDir of the home folder does not list notes.txt: %+v", got)
	}
	if _, err := dialHostVerbs(t, base).callWithin(15*time.Second, "wait-dir", map[string]any{"dir": farHome, "timeout": 200}); err != nil {
		t.Errorf("ASSERTION: wait-dir of the home folder failed: %v", err)
	}
	saveTransferArtifact(t, "link-dir-listings", record)
}

// TestALinkCannotReadAFileWithTwoNames is the review's finding 6: a hard link
// in the receive folder to the dummy key reads as the key, and no check of
// its path sees that. A linked machine must not read, hash, stream or copy
// a regular file that has more than one name.
func TestALinkCannotReadAFileWithTwoNames(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	farHome := xdgDir(remote, "HOME")
	writeRemoteConfig(t, remote, relayFilesPolicy)
	key := dummyKey(t, farHome)
	recv := filepath.Join(farHome, "Downloads", "tuios")
	if err := os.MkdirAll(recv, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(key, filepath.Join(recv, "keylink")); err != nil {
		t.Fatal(err)
	}
	writeFarFile(t, filepath.Join(recv, "single.txt"), "one name\n", 0o644)
	hubWithFileHost(t, base, remote, writeFakeSSHTo(t, base, remote))
	far := dialHostVerbs(t, base)
	record := map[string]any{}

	got, err := readContent(far, "~/Downloads/tuios/keylink")
	record["file-read"] = errString(err)
	if !forbidden(err) || strings.Contains(got, "DUMMY-KEY-MARKER") {
		t.Errorf("ASSERTION: a link read a file with two names: %q %v", got, err)
	}
	for verb, params := range map[string]map[string]any{
		"file-hash":        {"path": "~/Downloads/tuios/keylink"},
		"open-file-stream": {"path": "~/Downloads/tuios/keylink", "mode": "read"},
	} {
		c := dialHostVerbs(t, base)
		_, err := c.call(verb, params)
		record[verb] = errString(err)
		if !forbidden(err) {
			t.Errorf("ASSERTION: %s of a file with two names over the link was not refused: %v", verb, err)
		}
		_ = c.conn.Close()
	}

	// A copy the link starts reads its source on build, so it is held to the
	// same rule: it may start, and must end refused with nothing in place.
	var row transferRow
	raw, err := far.call("transfer-start", map[string]any{
		"src": map[string]any{"path": "~/Downloads/tuios/keylink"}, "dst": map[string]any{"path": "~/Downloads/tuios/copied"}, "conflict": "replace",
	})
	if err == nil {
		_ = json.Unmarshal(raw, &row)
		end := farTransfer(t, far, row.ID, 30*time.Second)
		record["transfer-start"] = end
		if end.State != "failed" || end.Code != "forbidden" {
			t.Errorf("ASSERTION: a copy of a file with two names ended %s (%s), want failed as forbidden: %s", end.State, end.Code, end.Error)
		}
	} else {
		record["transfer-start"] = errString(err)
		if !forbidden(err) {
			t.Errorf("ASSERTION: a copy of a file with two names was refused, but not as forbidden: %v", err)
		}
	}
	if b, err := os.ReadFile(filepath.Join(recv, "copied")); err == nil {
		t.Errorf("ASSERTION: the copy of a file with two names was put in place: %q", b)
	}

	// The positive half: a file with one name reads.
	if got, err := readContent(far, "~/Downloads/tuios/single.txt"); err != nil || got != "one name\n" {
		t.Errorf("ASSERTION: a read of a file with one name failed: %q %v", got, err)
	}
	if b, _ := os.ReadFile(key); string(b) != dummyKeyMarker {
		t.Errorf("ASSERTION: the dummy key changed: %q", b)
	}
	saveTransferArtifact(t, "link-two-names", record)
}

// TestADarwinLinkCannotReadThroughTheDataVolume: on macOS the folders of the
// data volume, /Users and /private among them, are also under
// /System/Volumes/Data. EvalSymlinks does not follow a firmlink, so a path
// there missed the deny list: a link read the dummy key and listed ~/.ssh
// through it. F_GETPATH names an open file by its short path, and realPath
// now does too, so both checks see ~/.ssh.
func TestADarwinLinkCannotReadThroughTheDataVolume(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("/System/Volumes/Data is a macOS folder")
	}
	base := t.TempDir()
	remote := remoteMachine(t)
	farHome := xdgDir(remote, "HOME")
	writeRemoteConfig(t, remote, listFilesPolicy)
	key := dummyKey(t, farHome)
	notes := filepath.Join(farHome, "notes.txt")
	writeFarFile(t, notes, "ordinary\n", 0o644)
	realKey, err := filepath.EvalSymlinks(key)
	if err != nil {
		t.Fatal(err)
	}
	realNotes, err := filepath.EvalSymlinks(notes)
	if err != nil {
		t.Fatal(err)
	}
	const data = "/System/Volumes/Data"
	if _, err := os.Stat(data + realKey); err != nil {
		t.Skipf("the home folder is not under %s: %v", data, err)
	}
	hubWithFileHost(t, base, remote, writeFakeSSHTo(t, base, remote))
	far := dialHostVerbs(t, base)

	got, err := readContent(far, data+realKey)
	if !forbidden(err) || strings.Contains(got, "DUMMY-KEY-MARKER") {
		t.Errorf("ASSERTION: a link read the dummy key through %s: %q %v", data, got, err)
	}
	record := map[string]any{"file-read": errString(err)}
	sshDir := data + filepath.Dir(realKey)
	for verb, params := range map[string]map[string]any{
		"file-stat": {"path": data + realKey},
		"file-list": {"dir": sshDir},
		"read-dir":  {"dir": sshDir},
	} {
		raw, err := far.call(verb, params)
		record[verb] = errString(err)
		if !forbidden(err) || strings.Contains(string(raw), "id_ed25519") {
			t.Errorf("ASSERTION: %s through %s over the link was not refused: %s %v", verb, data, raw, err)
		}
	}
	// The positive half: an ordinary file reads through the same folder.
	if got, err := readContent(far, data+realNotes); err != nil || got != "ordinary\n" {
		t.Errorf("ASSERTION: a read of an ordinary file through %s failed: %q %v", data, got, err)
	}
	saveTransferArtifact(t, "darwin-data-volume", record)
}
