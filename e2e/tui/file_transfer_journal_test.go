package tuie2e

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Copies that outlive their daemon, copies that share a destination, writes a
// linked machine may not make, and the events a copy sends.
//
// What would pass a weaker test and fail these: a job kept only in memory (a
// restart loses it, and its part stays as a hidden file), one part name per
// destination (two copies to one file write into each other and fail their
// check), a link write checked by its name and not its real path (a link in
// the home folder carries it into ~/.ssh), and a copy that polls instead of
// saying what it does (no transfer events, or one progress event per write).

// partsIn lists the part files in dir.
func partsIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") && strings.Contains(e.Name(), ".tuios-part") {
			out = append(out, e.Name())
		}
	}
	return out
}

// daemonPID is the pid of the daemon of base.
func daemonPID(t *testing.T, base string) int {
	t.Helper()
	var hello struct {
		PID int `json:"pid"`
	}
	dialFileVerbs(t, base).must("hello", nil, &hello)
	if hello.PID <= 0 {
		t.Fatalf("the daemon gave no pid")
	}
	return hello.PID
}

// TestACopySurvivesADaemonRestart copies 96 MiB from build over a link of
// 24 MB/s and kills the hub daemon with SIGKILL a third of the way in, as a
// crash would. The next daemon must find the copy in its journal, go on from
// the part the first one wrote, and put the original in place, checked. It
// must also remove a part in that folder that is eight days old and belongs
// to no copy, and keep one that is new.
func TestACopySurvivesADaemonRestart(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	const size = 96 << 20
	src := filepath.Join(remote, "big.bin")
	want := randomFile(t, src, size)

	env := hubWithFileHost(t, base, remote, writeSlowFakeSSH(t, base, remote, 24<<20, 1<<20))

	in := filepath.Join(base, "in")
	if err := os.MkdirAll(in, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(in, ".stale.bin.tuios-part-abcdef012345")
	young := filepath.Join(in, ".young.bin.tuios-part-0123456789ab")
	for _, p := range []string{stale, young} {
		if err := os.WriteFile(p, []byte("part"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	dst := filepath.Join(in, "big.bin")
	var row transferRow
	dialFileVerbs(t, base).must("transfer-start", map[string]any{
		"src": map[string]any{"host": "build", "path": src},
		"dst": map[string]any{"path": dst},
	}, &row)
	id := row.ID

	type point struct {
		Ms    int64  `json:"ms"`
		State string `json:"state"`
		Done  int64  `json:"done"`
		Note  string `json:"note,omitempty"`
	}
	var timeline []point
	start := time.Now()
	note := func(r transferRow, what string) {
		timeline = append(timeline, point{time.Since(start).Milliseconds(), r.State, r.Done, what})
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		row = transferNow(t, base, id)
		note(row, "")
		if row.Done >= size/3 {
			break
		}
		if row.State == "failed" || time.Now().After(deadline) {
			t.Fatalf("the copy never got a third of the way: %+v", row)
		}
		time.Sleep(50 * time.Millisecond)
	}
	atKill := row.Done
	pid := daemonPID(t, base)
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatalf("kill the hub daemon: %v", err)
	}
	for i := 0; i < 100 && syscall.Kill(pid, 0) == nil; i++ {
		time.Sleep(50 * time.Millisecond)
	}
	cutSlowLink(t, base)
	t.Logf("killed the hub daemon at %d of %d bytes", atKill, size)
	partAtKill := partsIn(t, in)

	if out, err := tuiosCLIEnv(t, base, env, "new", "again", "--detach"); err != nil {
		t.Fatalf("start the hub daemon again: %v\n%s", err, out)
	}
	var after struct {
		Transfers []transferRow `json:"transfers"`
	}
	raw, err := dialFileVerbs(t, base).call("transfer-list", map[string]any{"id": id})
	if err != nil {
		t.Fatalf("ASSERTION: the daemon that started again does not hold the copy: %v", err)
	}
	_ = json.Unmarshal(raw, &after)
	if len(after.Transfers) == 1 {
		note(after.Transfers[0], "after the restart")
	}

	deadline = time.Now().Add(120 * time.Second)
	for {
		row = transferNow(t, base, id)
		note(row, "")
		if row.State == "done" || row.State == "failed" || row.State == "cancelled" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: the copy did not finish after the restart: %+v", row)
		}
		time.Sleep(100 * time.Millisecond)
	}
	got := ""
	if row.State == "done" {
		got = fileSHA(t, dst)
	}
	left := partsIn(t, in)
	saveTransferArtifact(t, "transfer-restart", map[string]any{
		"killed_at": atKill, "size": size, "parts_at_kill": partAtKill, "parts_after": left,
		"final": row, "sha256_want": want, "sha256_in_place": got, "timeline": timeline,
	})
	t.Logf("final row: %+v", row)
	if row.State != "done" || !row.Verified {
		t.Fatalf("ASSERTION: the copy ended %s (verified %v): %s", row.State, row.Verified, row.Error)
	}
	if got != want {
		t.Fatalf("ASSERTION: the file in place is not the original: %s, want %s", got, want)
	}
	if row.ResumedFrom < atKill/2 {
		t.Fatalf("ASSERTION: the copy started over after the restart: resumed_from %d with %d bytes copied at the kill", row.ResumedFrom, atKill)
	}
	if slices.Contains(left, filepath.Base(stale)) {
		t.Errorf("ASSERTION: the eight day old part with no copy is still there: %v", left)
	}
	if !slices.Contains(left, filepath.Base(young)) {
		t.Errorf("ASSERTION: the new part was removed, which a copy of another daemon may still finish: %v", left)
	}
	if len(left) != 1 {
		t.Errorf("ASSERTION: the copy left its part behind: %v", left)
	}
}

// TestTwoCopiesToOnePathKeepTheirParts copies two files of 32 MiB from build
// to one file here at the same time, over a link of 24 MB/s, so both are in
// flight together. The two paths name the file through two folders, one a
// link to the other, so nothing but the part names keeps the copies apart.
// Each must end checked, and the file in place must be one of the two
// originals. A third copy to the first path, while the first runs, is
// refused as busy.
func TestTwoCopiesToOnePathKeepTheirParts(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	const size = 32 << 20
	a := filepath.Join(remote, "a.bin")
	b := filepath.Join(remote, "b.bin")
	wantA, wantB := randomFile(t, a, size), randomFile(t, b, size)
	hubWithFileHost(t, base, remote, writeSlowFakeSSH(t, base, remote, 24<<20, 1<<20))

	real := filepath.Join(base, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	c := dialFileVerbs(t, base)
	var ra, rb transferRow
	c.must("transfer-start", map[string]any{
		"src": map[string]any{"host": "build", "path": a}, "dst": map[string]any{"path": filepath.Join(real, "same.bin")}, "conflict": "replace",
	}, &ra)
	c.must("transfer-start", map[string]any{
		"src": map[string]any{"host": "build", "path": b}, "dst": map[string]any{"path": filepath.Join(alias, "same.bin")}, "conflict": "replace",
	}, &rb)
	_, busyErr := c.call("transfer-start", map[string]any{
		"src": map[string]any{"host": "build", "path": b}, "dst": map[string]any{"path": filepath.Join(real, "same.bin")}, "conflict": "replace",
	})
	var both []string
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		both = partsIn(t, real)
		if len(both) == 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	endA := waitTransferEnd(t, base, ra.ID, 90*time.Second)
	endB := waitTransferEnd(t, base, rb.ID, 90*time.Second)
	got := ""
	if _, err := os.Stat(filepath.Join(real, "same.bin")); err == nil {
		got = fileSHA(t, filepath.Join(real, "same.bin"))
	}
	left := partsIn(t, real)
	saveTransferArtifact(t, "two-copies-one-path", map[string]any{
		"a": endA, "b": endB, "parts_in_flight": both, "parts_after": left,
		"sha256_a": wantA, "sha256_b": wantB, "sha256_in_place": got, "busy": errString(busyErr),
	})
	var vf *verbFailure
	if !errors.As(busyErr, &vf) || vf.Code != "busy" {
		t.Errorf("ASSERTION: a third copy to the path the first writes was not refused as busy: %v", busyErr)
	}
	if len(both) != 2 {
		t.Errorf("ASSERTION: the two copies did not write two parts at once: %v", both)
	}
	for _, e := range []transferRow{endA, endB} {
		if e.State != "done" || !e.Verified {
			t.Errorf("ASSERTION: copy %s ended %s (verified %v): %s", e.ID, e.State, e.Verified, e.Error)
		}
	}
	if got != wantA && got != wantB {
		t.Errorf("ASSERTION: the file in place is neither original: %s", got)
	}
	if len(left) != 0 {
		t.Errorf("ASSERTION: parts were left behind: %v", left)
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// TestALinkCannotWriteKeysOrLeaveTheHome sends files from this machine to
// build where they must not land: build's authorized_keys, by name, through
// a link in build's home that points at ~/.ssh, and with the folder's name in
// capitals; build's .bashrc; and a folder outside build's home. Each copy
// must fail as forbidden and leave the file there as it was, with no part
// beside it. The verbs that change files on build are refused the same way
// when they name those places. The positive half: a copy to a folder in
// build's home arrives, checked, with its time kept, and a rename and a
// remove there work.
func TestALinkCannotWriteKeysOrLeaveTheHome(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	farHome := xdgDir(remote, "HOME")
	keys := filepath.Join(farHome, ".ssh", "authorized_keys")
	rc := filepath.Join(farHome, ".bashrc")
	if err := os.MkdirAll(filepath.Dir(keys), 0o700); err != nil {
		t.Fatal(err)
	}
	const keep = "ssh-ed25519 AAAA the person's own key\n"
	if err := os.WriteFile(keys, []byte(keep), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rc, []byte(keep), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(farHome, ".ssh"), filepath.Join(farHome, "innocent")); err != nil {
		t.Fatal(err)
	}
	hubWithFileHost(t, base, remote, writeFakeSSHTo(t, base, remote))

	evil := filepath.Join(base, "evil.pub")
	if err := os.WriteFile(evil, []byte("ssh-ed25519 AAAA the attacker's key\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		Dst   string `json:"dst"`
		State string `json:"state"`
		Code  string `json:"code"`
		Error string `json:"error"`
	}
	var copies []outcome
	c := dialFileVerbs(t, base)
	for _, dst := range []string{
		"~/.ssh/authorized_keys",
		keys,
		filepath.Join(farHome, "innocent", "authorized_keys"),
		filepath.Join(farHome, ".SSH", "authorized_keys"),
		rc,
		filepath.Join(remote, "outside.txt"),
	} {
		var row transferRow
		c.must("transfer-start", map[string]any{
			"src": map[string]any{"path": evil}, "dst": map[string]any{"host": "build", "path": dst}, "conflict": "replace",
		}, &row)
		row = waitTransferEnd(t, base, row.ID, 30*time.Second)
		copies = append(copies, outcome{dst, row.State, row.Code, row.Error})
		if row.State != "failed" || row.Code != "forbidden" {
			t.Errorf("ASSERTION: the copy to build:%s ended %s (%s), want failed as forbidden", dst, row.State, row.Code)
		}
	}

	far := dialHostVerbs(t, base)
	type refusal struct {
		Verb   string         `json:"verb"`
		Params map[string]any `json:"params"`
		Answer string         `json:"answer"`
	}
	var verbs []refusal
	for _, v := range []refusal{
		{Verb: "file-mkdir", Params: map[string]any{"path": "~/.ssh/more"}},
		{Verb: "file-mkdir", Params: map[string]any{"path": filepath.Join(remote, "elsewhere")}},
		{Verb: "file-remove", Params: map[string]any{"path": "~/.ssh/authorized_keys"}},
		{Verb: "file-remove", Params: map[string]any{"path": "~/innocent/authorized_keys"}},
		{Verb: "file-rename", Params: map[string]any{"from": "~/.ssh/authorized_keys", "to": "~/stolen"}},
		{Verb: "file-rename", Params: map[string]any{"from": "~/innocent", "to": "~/.config/autostart", "replace": true}},
		{Verb: "open-file-stream", Params: map[string]any{"path": "~/.bashrc", "mode": "write", "length": 4}},
	} {
		_, err := far.call(v.Verb, v.Params)
		v.Answer = errString(err)
		verbs = append(verbs, v)
		var vf *verbFailure
		if !errors.As(err, &vf) || vf.Code != "forbidden" {
			t.Errorf("ASSERTION: %s %v over the link was not refused as forbidden: %v", v.Verb, v.Params, err)
		}
	}

	for _, p := range []string{keys, rc} {
		if got, _ := os.ReadFile(p); string(got) != keep {
			t.Errorf("ASSERTION: %s changed: %q", p, got)
		}
	}
	for _, dir := range []string{filepath.Dir(keys), farHome, remote} {
		if left := partsIn(t, dir); len(left) > 0 {
			t.Errorf("ASSERTION: a refused copy left a part in %s: %v", dir, left)
		}
	}
	if _, err := os.Stat(filepath.Join(remote, "outside.txt")); err == nil {
		t.Errorf("ASSERTION: a file arrived outside build's home")
	}

	// The positive half.
	far.must("file-mkdir", map[string]any{"path": "~/inbox"}, nil)
	mtime := time.Date(2023, 7, 1, 8, 0, 0, 0, time.UTC)
	if err := os.Chtimes(evil, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	var ok transferRow
	c.must("transfer-start", map[string]any{
		"src": map[string]any{"path": evil}, "dst": map[string]any{"host": "build", "path": "~/inbox/key.pub"},
	}, &ok)
	ok = waitTransferEnd(t, base, ok.ID, 30*time.Second)
	arrived := filepath.Join(farHome, "inbox", "key.pub")
	fi, statErr := os.Stat(arrived)
	if ok.State != "done" || !ok.Verified || statErr != nil {
		t.Fatalf("ASSERTION: the copy to build:~/inbox ended %s (verified %v): %s %v", ok.State, ok.Verified, ok.Error, statErr)
	}
	if !fi.ModTime().Equal(mtime) {
		t.Errorf("ASSERTION: the copy on build does not keep the original's time: %v, want %v", fi.ModTime().UTC(), mtime)
	}
	// A link inside the home folder to another folder there is a fine
	// place to write: the write lands where the link points.
	if err := os.Symlink(filepath.Join(farHome, "inbox"), filepath.Join(farHome, "projects")); err != nil {
		t.Fatal(err)
	}
	var via transferRow
	c.must("transfer-start", map[string]any{
		"src": map[string]any{"path": evil}, "dst": map[string]any{"host": "build", "path": "~/projects/via-link.pub"},
	}, &via)
	via = waitTransferEnd(t, base, via.ID, 30*time.Second)
	if _, err := os.Stat(filepath.Join(farHome, "inbox", "via-link.pub")); via.State != "done" || err != nil {
		t.Errorf("ASSERTION: the copy through a link inside build's home ended %s (%s): %v", via.State, via.Error, err)
	}
	far.must("file-rename", map[string]any{"from": "~/inbox/key.pub", "to": "~/inbox/renamed.pub"}, nil)
	far.must("file-remove", map[string]any{"path": "~/inbox/renamed.pub"}, nil)
	if _, err := os.Stat(filepath.Join(farHome, "inbox", "renamed.pub")); err == nil {
		t.Errorf("ASSERTION: the remove in build's home did nothing")
	}
	saveTransferArtifact(t, "link-write-confinement", map[string]any{"copies": copies, "verbs": verbs, "allowed": ok})
}

// TestFileWritesNeedTheFilesCapability gives the hub list and write on build,
// but not files. A listing goes through, every file verb but the listing is
// refused on its own, and a copy to build fails as forbidden and names the
// capability that is missing.
func TestFileWritesNeedTheFilesCapability(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	writeRemoteConfig(t, remote, "[hosts.\"*\"]\nallow = [\"list\", \"write\"]\n")
	hubWithFileHost(t, base, remote, writeFakeSSHTo(t, base, remote))
	src := filepath.Join(base, "note.txt")
	if err := os.WriteFile(src, []byte("note\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	far := dialHostVerbs(t, base)
	far.must("file-list", map[string]any{"dir": "~"}, nil)
	// Each verb on its own, so one that needs only write is seen.
	for _, v := range []struct {
		verb   string
		params map[string]any
	}{
		{"file-read", map[string]any{"path": "~/x"}},
		{"file-hash", map[string]any{"path": "~/x"}},
		{"file-walk", map[string]any{"path": "~"}},
		{"open-file-stream", map[string]any{"path": "~/x", "mode": "write", "length": 1}},
		{"file-mkdir", map[string]any{"path": "~/new"}},
		{"file-rename", map[string]any{"from": "~/a", "to": "~/b"}},
		{"file-remove", map[string]any{"path": "~/a"}},
		{"file-commit", map[string]any{"path": "~/x"}},
		{"file-abort", map[string]any{"path": "~/x"}},
		{"file-drop-dir", map[string]any{}},
	} {
		_, err := far.call(v.verb, v.params)
		var vf *verbFailure
		if !errors.As(err, &vf) || vf.Code != "forbidden" || !strings.Contains(vf.Message+errString(err), "files") {
			t.Errorf("ASSERTION: %s on a host that does not allow files answered %v, want forbidden naming files", v.verb, err)
		}
	}
	var row transferRow
	dialFileVerbs(t, base).must("transfer-start", map[string]any{
		"src": map[string]any{"path": src}, "dst": map[string]any{"host": "build", "path": "~/note.txt"},
	}, &row)
	row = waitTransferEnd(t, base, row.ID, 30*time.Second)
	saveTransferArtifact(t, "files-capability", row)
	if row.State != "failed" || row.Code != "forbidden" || !strings.Contains(row.Error, "files") {
		t.Fatalf("ASSERTION: a copy to a host that does not allow files ended %s (%s): %s", row.State, row.Code, row.Error)
	}
	if _, err := os.Stat(filepath.Join(xdgDir(remote, "HOME"), "note.txt")); err == nil {
		t.Fatalf("ASSERTION: the file arrived anyway")
	}
}

// eventLine is one line of the event stream, as far as these tests read it.
type eventLine struct {
	Type     string      `json:"type"`
	Action   string      `json:"action"`
	Transfer transferRow `json:"transfer"`
	Ms       int64       `json:"ms"`
}

// subscribeTransfers opens an event stream on the daemon of base. types may
// be nil for every type a plain subscriber gets.
func subscribeTransfers(t *testing.T, base string, types []string) (*bufio.Reader, net.Conn) {
	t.Helper()
	c := dialFileVerbs(t, base)
	params := map[string]any{}
	if types != nil {
		params["types"] = types
	}
	c.must("subscribe", params, nil)
	_ = c.conn.SetDeadline(time.Time{})
	return c.br, c.conn
}

// readTransferEvents reads the transfer events of id until it ends.
func readTransferEvents(t *testing.T, br *bufio.Reader, conn net.Conn, id string, within time.Duration) []eventLine {
	t.Helper()
	start := time.Now()
	_ = conn.SetReadDeadline(time.Now().Add(within))
	var out []eventLine
	for {
		line, err := br.ReadBytes('\n')
		if err != nil {
			t.Logf("the event stream ended: %v", err)
			return out
		}
		var ev eventLine
		if json.Unmarshal(line, &ev) != nil || !strings.HasPrefix(ev.Type, "transfer") || ev.Transfer.ID != id {
			continue
		}
		ev.Ms = time.Since(start).Milliseconds()
		out = append(out, ev)
		if ev.Type == "transfer" && ev.Action == "ended" {
			return out
		}
	}
}

// TestACopySaysWhatItDoesOnTheEventStream copies 16 MiB from build over a
// link of 8 MB/s with two subscribers on the hub: one that names the
// transfer types, and one that names none. The first must see the copy
// created, its progress at most four times a second, and its end, done and
// checked. The second must see the same changes and no progress.
func TestACopySaysWhatItDoesOnTheEventStream(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	src := filepath.Join(remote, "events.bin")
	randomFile(t, src, 16<<20)
	hubWithFileHost(t, base, remote, writeSlowFakeSSH(t, base, remote, 8<<20, 1<<20))

	named, namedConn := subscribeTransfers(t, base, []string{"transfer", "transfer-progress"})
	plain, plainConn := subscribeTransfers(t, base, nil)
	var row transferRow
	dialFileVerbs(t, base).must("transfer-start", map[string]any{
		"src": map[string]any{"host": "build", "path": src}, "dst": map[string]any{"path": filepath.Join(base, "events.bin")},
	}, &row)
	evs := readTransferEvents(t, named, namedConn, row.ID, 60*time.Second)
	plainEvs := readTransferEvents(t, plain, plainConn, row.ID, 10*time.Second)
	saveTransferArtifact(t, "transfer-events", map[string]any{"named": evs, "plain": plainEvs})

	if len(evs) < 2 || evs[0].Type != "transfer" || evs[0].Action != "created" {
		t.Fatalf("ASSERTION: the stream does not start with the copy created: %+v", evs)
	}
	last := evs[len(evs)-1]
	if last.Action != "ended" || last.Transfer.State != "done" || !last.Transfer.Verified {
		t.Fatalf("ASSERTION: the stream does not end with the copy done and checked: %+v", last)
	}
	var progress []eventLine
	var done int64
	for _, ev := range evs {
		if ev.Type != "transfer-progress" {
			continue
		}
		if ev.Transfer.Done < done {
			t.Errorf("ASSERTION: progress went back from %d to %d", done, ev.Transfer.Done)
		}
		done = ev.Transfer.Done
		progress = append(progress, ev)
	}
	if len(progress) < 3 {
		t.Errorf("ASSERTION: a copy of two seconds sent %d progress events, want at least 3", len(progress))
	}
	if len(progress) >= 2 {
		span := float64(progress[len(progress)-1].Ms-progress[0].Ms) / 1000
		if span > 0 && float64(len(progress)-1)/span > 5 {
			t.Errorf("ASSERTION: %d progress events in %.1f s, more than four a second", len(progress), span)
		}
	}
	for _, ev := range plainEvs {
		if ev.Type == "transfer-progress" {
			t.Fatalf("ASSERTION: a subscriber that named no types got a progress event")
		}
	}
	if len(plainEvs) < 2 || plainEvs[0].Action != "created" || plainEvs[len(plainEvs)-1].Action != "ended" {
		t.Fatalf("ASSERTION: a subscriber that named no types did not see the copy start and end: %+v", plainEvs)
	}
}
