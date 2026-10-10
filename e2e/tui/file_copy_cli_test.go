package tuie2e

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	mrand "math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// tuios cp and tuios transfers, against two real daemons.
//
// The far machine is build, the second daemon of host_attach_test.go, reached
// through the hub's link and the ssh stand-in. Every test runs the real tuios
// binary as a person or a script would, reads its exit code and its output,
// and checks every file it copied byte for byte. Each saves what it saw under
// TUIOS_E2E_FRAMES: the NDJSON of each run, the exit codes, and the sha256 of
// the files on both machines.
//
// What would pass a weaker test and fail these: a copy that reports done
// before the bytes are in place (the files are hashed on both machines), a
// second run that sends the bytes again (it must move almost nothing over the
// link), a conflict policy that writes over a file it should leave (the file's
// bytes are checked), an exit code that says success for a partial copy, a
// cancel that leaves the copy running, and a pane that copies without the
// grant.

// cpRun is one run of the tuios CLI.
type cpRun struct {
	Args   []string `json:"args"`
	Code   int      `json:"code"`
	Stdout string   `json:"stdout"`
	Stderr string   `json:"stderr"`
	Ms     int64    `json:"ms"`
}

// runTuios runs the tuios binary against the hub under base, with stdout and
// stderr apart, and returns its exit code. It is not a terminal.
func runTuios(t *testing.T, base string, args ...string) cpRun {
	t.Helper()
	pinPreV080Looks(t, base)
	cmd := exec.Command(tuiosBin, args...)
	cmd.Dir = workDirIn(t, base)
	cmd.Env = cliEnv(base)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	err := cmd.Run()
	r := cpRun{Args: args, Stdout: stdout.String(), Stderr: stderr.String(), Ms: time.Since(start).Milliseconds()}
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		r.Code = ee.ExitCode()
	default:
		t.Fatalf("run tuios %v: %v", args, err)
	}
	return r
}

// cliEnv is the environment of a tuios command for the hub under base. The
// pane variables of a tuios the suite runs in are left out, so the command
// is the person's, outside every pane.
func cliEnv(base string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "TUIOS_") && !strings.HasPrefix(kv, "TUIOS_E2E") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "SHELL=/bin/sh")
	for _, key := range xdgKeys {
		env = append(env, key+"="+xdgDir(base, key))
	}
	return env
}

// cpEvents decodes the NDJSON a --json run printed.
func cpEvents(t *testing.T, r cpRun) []map[string]any {
	t.Helper()
	var out []map[string]any
	sc := bufio.NewScanner(strings.NewReader(r.Stdout))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("ASSERTION: a --json line is not JSON: %q (%v)", line, err)
		}
		out = append(out, ev)
	}
	return out
}

func eventsNamed(evs []map[string]any, name string) []map[string]any {
	var out []map[string]any
	for _, ev := range evs {
		if ev["event"] == name {
			out = append(out, ev)
		}
	}
	return out
}

// treeSums is the sha256 of every regular file under root, by its path
// under root, and the mode and time of each.
type fileFacts struct {
	SHA   string `json:"sha256"`
	Perm  uint32 `json:"perm"`
	MTime int64  `json:"mtime"`
}

func treeSums(t *testing.T, root string) map[string]fileFacts {
	t.Helper()
	out := map[string]fileFacts{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() || strings.Contains(d.Name(), ".tuios-part") {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out[filepath.ToSlash(rel)] = fileFacts{SHA: fileSHA(t, p), Perm: uint32(fi.Mode().Perm()), MTime: fi.ModTime().UnixMilli()}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return out
}

// sameTrees fails the test when two trees do not hold the same files with
// the same bytes, permission bits and times.
func sameTrees(t *testing.T, what string, want, got map[string]fileFacts) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("ASSERTION: %s: %d files, want %d", what, len(got), len(want))
	}
	for rel, w := range want {
		g, ok := got[rel]
		switch {
		case !ok:
			t.Fatalf("ASSERTION: %s: %s is missing", what, rel)
		case g.SHA != w.SHA:
			t.Fatalf("ASSERTION: %s: %s has other bytes", what, rel)
		case g.Perm != w.Perm:
			t.Fatalf("ASSERTION: %s: %s has mode %o, want %o", what, rel, g.Perm, w.Perm)
		case g.MTime != w.MTime:
			t.Fatalf("ASSERTION: %s: %s has time %d, want %d", what, rel, g.MTime, w.MTime)
		}
	}
}

// makeSmallTree writes n small files in folders of 100 under root, half text
// that compresses and half random bytes, with sizes from 0 to 6 KiB, some
// with modes and times of their own. It returns the bytes written.
func makeSmallTree(t *testing.T, root string, n int, seed uint64) int64 {
	t.Helper()
	rng := mrand.New(mrand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	var total int64
	words := []string{"tuios", "copy", "file", "folder", "machine", "link", "check", "bytes", "pane", "host"}
	for i := range n {
		dir := filepath.Join(root, fmt.Sprintf("d%02d", i/100), fmt.Sprintf("s%d", i%3))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		size := rng.IntN(6 << 10)
		if i%250 == 0 {
			size = 0
		}
		data := make([]byte, size)
		if i%2 == 0 {
			var b strings.Builder
			for b.Len() < size {
				b.WriteString(words[rng.IntN(len(words))] + " ")
			}
			copy(data, b.String())
		} else {
			_, _ = rand.Read(data)
		}
		p := filepath.Join(dir, fmt.Sprintf("f%04d.txt", i))
		mode := os.FileMode(0o644)
		if i%7 == 0 {
			mode = 0o600
		}
		if err := os.WriteFile(p, data, mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		at := time.Date(2025, 1, 1+i%28, 10, i%60, 0, 0, time.UTC)
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
		total += int64(size)
	}
	return total
}

// farHome is build's home folder.
func farHome(remote string) string { return xdgDir(remote, "HOME") }

// TestCopyAFileAndAFolderToAHostAndBack copies one file and a folder of 2,000
// small files and one large one to build with tuios cp, and the folder back
// to a new folder here, then runs the same copy again. Every file must be
// byte for byte the original with its mode and time, the --json events must
// parse and end in done, and the second run must find every file the same
// and move almost nothing over the link.
func TestCopyAFileAndAFolderToAHostAndBack(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	hubWithFileHost(t, base, remote, writeFakeSSHTo(t, base, remote))

	src := filepath.Join(base, "work")
	tree := filepath.Join(src, "tree")
	makeSmallTree(t, tree, 2000, 1)
	big := filepath.Join(tree, "big", "image.bin")
	if err := os.MkdirAll(filepath.Dir(big), 0o755); err != nil {
		t.Fatal(err)
	}
	randomFile(t, big, 12<<20)
	one := filepath.Join(src, "notes.txt")
	if err := os.WriteFile(one, []byte(strings.Repeat("a note to copy\n", 4000)), 0o640); err != nil {
		t.Fatal(err)
	}
	want := treeSums(t, tree)
	artifact := map[string]any{}

	// One file into the host's home folder.
	r := runTuios(t, base, "cp", "--json", one, "build:")
	artifact["file_up"] = r
	if r.Code != 0 {
		t.Fatalf("ASSERTION: cp of a file to build exited %d\n%s\n%s", r.Code, r.Stdout, r.Stderr)
	}
	evs := cpEvents(t, r)
	if len(eventsNamed(evs, "started")) != 1 || len(eventsNamed(evs, "done")) != 1 || evs[len(evs)-1]["event"] != "done" {
		t.Fatalf("ASSERTION: the events of the file copy do not start and end in done: %v", evs)
	}
	if got := fileSHA(t, filepath.Join(farHome(remote), "notes.txt")); got != fileSHA(t, one) {
		t.Fatalf("ASSERTION: notes.txt on build is not the original")
	}
	if done := eventsNamed(evs, "done")[0]; done["wire_bytes"].(float64) >= float64(len(strings.Repeat("a note to copy\n", 4000)))/2 {
		t.Fatalf("ASSERTION: a text file that compresses well crossed the link at %v bytes, not compressed", done["wire_bytes"])
	}

	// The folder up.
	r = runTuios(t, base, "cp", "--json", tree, "build:~/")
	artifact["tree_up"] = r
	if r.Code != 0 {
		t.Fatalf("ASSERTION: cp of a folder to build exited %d\n%s", r.Code, r.Stderr)
	}
	evs = cpEvents(t, r)
	done := eventsNamed(evs, "done")
	if len(done) != 1 || evs[len(evs)-1]["event"] != "done" {
		t.Fatalf("ASSERTION: the folder copy's events do not end in done: %v", evs[len(evs)-1])
	}
	doneFiles := 0
	for _, ev := range eventsNamed(evs, "file") {
		if ev["state"] != "done" || len(ev["sha256"].(string)) != 64 {
			t.Fatalf("ASSERTION: a clean copy reported a file that did not copy: %v", ev)
		}
		doneFiles++
	}
	if doneFiles != len(want) {
		t.Fatalf("ASSERTION: the copy reported %d files done, want %d", doneFiles, len(want))
	}
	sameTrees(t, "the folder on build", want, treeSums(t, filepath.Join(farHome(remote), "tree")))

	// The same copy again: everything is there with the same bytes.
	r = runTuios(t, base, "cp", "--json", tree, "build:~/")
	artifact["tree_up_again"] = r
	if r.Code != 0 {
		t.Fatalf("ASSERTION: the second cp exited %d\n%s", r.Code, r.Stderr)
	}
	again := eventsNamed(cpEvents(t, r), "done")
	if len(again) != 1 || int(again[0]["same"].(float64)) != len(want) {
		t.Fatalf("ASSERTION: the second run did not find every file the same: %v", again)
	}
	if wire := again[0]["wire_bytes"].(float64); wire > 2<<20 {
		t.Fatalf("ASSERTION: the second run moved %.0f bytes over the link, want almost none", wire)
	}

	// And back, to a folder that does not exist yet: it is the new name.
	back := filepath.Join(base, "back")
	r = runTuios(t, base, "cp", "build:tree", back)
	artifact["tree_down"] = r
	if r.Code != 0 {
		t.Fatalf("ASSERTION: cp of a folder from build exited %d\n%s", r.Code, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "Copied 2,001 files") || !strings.Contains(r.Stdout, "(checked)") {
		t.Fatalf("ASSERTION: the finish line does not say what was copied: %q", r.Stdout)
	}
	sameTrees(t, "the folder copied back", want, treeSums(t, back))
	artifact["sha256"] = want
	saveTransferArtifact(t, "cp-round-trip", artifact)
}

// TestCopyConflictsFollowThePolicy copies a folder onto a copy of it on build
// in which three files differ, once per policy. skip leaves them and exits 3
// when it was the default and 0 when it was asked for; replace writes them;
// keep-both keeps them and adds "name (from HOST).ext" beside them. A file
// with the same bytes is never a conflict.
func TestCopyConflictsFollowThePolicy(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	hubWithFileHost(t, base, remote, writeFakeSSHTo(t, base, remote))
	host, _ := os.Hostname()
	host, _, _ = strings.Cut(host, ".")

	src := filepath.Join(base, "site")
	makeSmallTree(t, src, 60, 2)
	changed := []string{"d00/s0/f0003.txt", "d00/s1/f0004.txt", "d00/s2/f0005.txt"}
	far := filepath.Join(farHome(remote), "site")
	reset := func() {
		_ = os.RemoveAll(far)
		if r := runTuios(t, base, "cp", "--conflict", "replace", src, "build:"); r.Code != 0 {
			t.Fatalf("seed copy exited %d: %s", r.Code, r.Stderr)
		}
		for _, rel := range changed {
			if err := os.WriteFile(filepath.Join(far, filepath.FromSlash(rel)), []byte("changed on build\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	artifact := map[string]any{}
	check := func(what string, r cpRun, code int, keep bool) {
		t.Helper()
		artifact[what] = r
		if r.Code != code {
			t.Fatalf("ASSERTION: %s exited %d, want %d\n%s\n%s", what, r.Code, code, r.Stdout, r.Stderr)
		}
		for _, rel := range changed {
			got, _ := os.ReadFile(filepath.Join(far, filepath.FromSlash(rel)))
			if keep != (string(got) == "changed on build\n") {
				t.Fatalf("ASSERTION: %s: %s on build holds %q", what, rel, got)
			}
		}
	}

	reset()
	r := runTuios(t, base, "cp", src, "build:")
	check("default skip", r, 3, true)
	if !strings.Contains(r.Stdout, "3 files were there and differ, so they were skipped") || !strings.Contains(r.Stdout, changed[0]) {
		t.Fatalf("ASSERTION: the skipped files are not listed: %q", r.Stdout)
	}
	check("explicit skip", runTuios(t, base, "cp", "-n", src, "build:"), 0, true)
	check("replace", runTuios(t, base, "cp", "--conflict", "replace", src, "build:"), 0, false)
	sameTrees(t, "the folder after replace", treeSums(t, src), treeSums(t, far))

	reset()
	r = runTuios(t, base, "cp", "--json", "-b", src, "build:")
	check("keep-both", r, 0, true)
	for _, rel := range changed {
		ext := filepath.Ext(rel)
		kept := filepath.Join(far, filepath.FromSlash(strings.TrimSuffix(rel, ext)+" (from "+host+")"+ext))
		if fileSHA(t, kept) != fileSHA(t, filepath.Join(src, filepath.FromSlash(rel))) {
			t.Fatalf("ASSERTION: keep-both did not put the new %s beside the old one as %s", rel, kept)
		}
	}
	done := eventsNamed(cpEvents(t, r), "done")
	if len(done) != 1 || int(done[0]["same"].(float64)) != 57 {
		t.Fatalf("ASSERTION: keep-both did not find the 57 other files the same: %v", done)
	}

	// One file onto one that differs, with no policy and no terminal.
	one := filepath.Join(src, "d00", "s0", "f0003.txt")
	r = runTuios(t, base, "cp", one, "build:site/d00/s0/")
	check("one file default", r, 3, true)
	saveTransferArtifact(t, "cp-conflicts", artifact)
}

// TestCopyExitCodes gives every exit code of tuios cp from a real case: a
// copy that works, a source that is not there, a wrong command line, a skip
// by default, a host that refuses, a host that stays away past --timeout,
// a file that changes while it is copied, and Ctrl+C.
func TestCopyExitCodes(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	ssh := writeSlowFakeSSH(t, base, remote, 4<<20, 1<<20)
	hubWithFileHost(t, base, remote, ssh)
	codes := map[string]cpRun{}
	expect := func(name string, r cpRun, code int) {
		t.Helper()
		codes[name] = r
		if r.Code != code {
			t.Fatalf("ASSERTION: %s exited %d, want %d\nstdout: %s\nstderr: %s", name, r.Code, code, r.Stdout, r.Stderr)
		}
	}
	defer func() { saveTransferArtifact(t, "cp-exit-codes", codes) }()

	small := filepath.Join(base, "small.txt")
	if err := os.WriteFile(small, []byte("small\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	expect("0 copied", runTuios(t, base, "cp", small, "build:"), 0)
	expect("0 the same", runTuios(t, base, "cp", small, "build:"), 0)
	expect("1 no such file on the host", runTuios(t, base, "cp", "build:~/not-there.txt", base+"/"), 1)
	expect("2 one argument", runTuios(t, base, "cp", small), 2)
	expect("2 unknown host", runTuios(t, base, "cp", small, "biuld:"), 2)
	if r := codes["2 unknown host"]; !strings.Contains(r.Stderr, "did you mean build:") {
		t.Fatalf("ASSERTION: an unknown host does not name the closest one: %s", r.Stderr)
	}
	expect("2 bad flag", runTuios(t, base, "cp", "--no-such-flag", small, "build:"), 2)
	if err := os.WriteFile(filepath.Join(farHome(remote), "small.txt"), []byte("other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	expect("3 skipped by default", runTuios(t, base, "cp", small, "build:"), 3)

	// 6: the file grows while its bytes move.
	// The stand-in slows what build sends, so these copies come from build.
	here := filepath.Join(base, "here")
	if err := os.MkdirAll(here, 0o755); err != nil {
		t.Fatal(err)
	}
	growing := filepath.Join(farHome(remote), "growing.bin")
	randomFile(t, growing, 24<<20)
	cmd := exec.Command(tuiosBin, "cp", "--json", "build:growing.bin", here+"/")
	cmd.Dir, cmd.Env = workDirIn(t, base), cliEnv(base)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	f, err := os.OpenFile(growing, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write([]byte("more bytes after the copy started\n"))
	_ = f.Close()
	err = cmd.Wait()
	var ee *exec.ExitError
	r := cpRun{Args: cmd.Args, Stdout: out.String()}
	if errors.As(err, &ee) {
		r.Code = ee.ExitCode()
	}
	expect("6 the source changed", r, 6)
	if !strings.Contains(r.Stdout, "source_changed") {
		t.Fatalf("ASSERTION: the failure does not say source_changed: %s", r.Stdout)
	}

	// 130: Ctrl+C on a cp that is not on a terminal cancels it.
	randomFile(t, filepath.Join(farHome(remote), "slow.bin"), 32<<20)
	cmd = exec.Command(tuiosBin, "cp", "--json", "build:slow.bin", filepath.Join(here, "slow-copy.bin"))
	cmd.Dir, cmd.Env = workDirIn(t, base), cliEnv(base)
	out.Reset()
	cmd.Stdout = &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	_ = cmd.Process.Signal(syscall.SIGINT)
	err = cmd.Wait()
	r = cpRun{Args: cmd.Args, Stdout: out.String()}
	if errors.As(err, &ee) {
		r.Code = ee.ExitCode()
	}
	expect("130 Ctrl+C", r, 130)
	if _, err := os.Stat(filepath.Join(here, "slow-copy.bin")); err == nil {
		t.Fatalf("ASSERTION: a cancelled copy put its file in place")
	}
	if parts, _ := filepath.Glob(filepath.Join(here, ".slow-copy.bin.tuios-part*")); len(parts) > 0 {
		t.Fatalf("ASSERTION: a cancelled copy left its part: %v", parts)
	}

	// 5: the link is cut and stays cut past --timeout. The copy waits in
	// the daemon after the command gives up.
	cmd = exec.Command(tuiosBin, "cp", "--json", "--timeout", "3s", "build:slow.bin", filepath.Join(here, "slow-two.bin"))
	cmd.Dir, cmd.Env = workDirIn(t, base), cliEnv(base)
	out.Reset()
	cmd.Stdout = &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1200 * time.Millisecond)
	// Cut and keep cutting, so the link stays down.
	stopCut := make(chan struct{})
	go func() {
		for {
			cutSlowLink(t, base)
			select {
			case <-stopCut:
				return
			case <-time.After(200 * time.Millisecond):
			}
		}
	}()
	err = cmd.Wait()
	close(stopCut)
	r = cpRun{Args: cmd.Args, Stdout: out.String()}
	if errors.As(err, &ee) {
		r.Code = ee.ExitCode()
	}
	expect("5 host away past --timeout", r, 5)
	if len(eventsNamed(cpEvents(t, r), "waiting")) == 0 {
		t.Fatalf("ASSERTION: the copy never said it was waiting: %s", r.Stdout)
	}

	// 4: build lets this machine list and nothing more.
	writeRemoteConfig(t, remote, "[hosts.\"*\"]\nallow = [\"list\"]\n")
	waitForHostListing(t, base, func(s string) bool { return strings.Contains(s, "build") && strings.Contains(s, "up") }, "build did not come back up")
	deadline := time.Now().Add(20 * time.Second)
	var refused cpRun
	for {
		refused = runTuios(t, base, "cp", "--conflict", "replace", small, "build:")
		if refused.Code == 4 || time.Now().After(deadline) {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	expect("4 refused by the link policy", refused, 4)
	if !strings.Contains(refused.Stderr, "refused") || !strings.Contains(refused.Stderr, "decision") {
		t.Fatalf("ASSERTION: the refusal does not say why: %s", refused.Stderr)
	}
}

// TestCopyPauseResumeAndCancel starts copies with --detach over a slow link
// and controls them with tuios transfers: a paused copy moves no bytes, a
// resumed one ends checked, and a cancelled one ends with its part removed
// and tuios transfers wait exiting 130.
func TestCopyPauseResumeAndCancel(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	hubWithFileHost(t, base, remote, writeSlowFakeSSH(t, base, remote, 4<<20, 1<<20))
	// The stand-in slows what build sends, so the copies come from build.
	big := filepath.Join(farHome(remote), "big.bin")
	want := randomFile(t, big, 24<<20)
	artifact := map[string]any{}
	here := filepath.Join(base, "here")
	if err := os.MkdirAll(here, 0o755); err != nil {
		t.Fatal(err)
	}

	r := runTuios(t, base, "cp", "--detach", "build:big.bin", here+"/")
	if r.Code != 0 {
		t.Fatalf("cp --detach exited %d: %s", r.Code, r.Stderr)
	}
	id := strings.TrimSpace(r.Stdout)
	time.Sleep(time.Second)
	if p := runTuios(t, base, "transfers", "pause", id); p.Code != 0 || !strings.Contains(p.Stdout, "is paused") {
		t.Fatalf("ASSERTION: transfers pause: %d %s %s", p.Code, p.Stdout, p.Stderr)
	}
	time.Sleep(500 * time.Millisecond)
	a := transferNow(t, base, id)
	time.Sleep(1500 * time.Millisecond)
	b := transferNow(t, base, id)
	artifact["paused"] = []transferRow{a, b}
	if a.State != "paused" || b.Done != a.Done {
		t.Fatalf("ASSERTION: a paused copy moved bytes: %+v then %+v", a, b)
	}
	list := runTuios(t, base, "transfers")
	if !strings.Contains(list.Stdout, id) || !strings.Contains(list.Stdout, "paused") {
		t.Fatalf("ASSERTION: tuios transfers does not list the paused copy: %s", list.Stdout)
	}
	if p := runTuios(t, base, "transfers", "resume", id); p.Code != 0 {
		t.Fatalf("transfers resume exited %d: %s", p.Code, p.Stderr)
	}
	w := runTuios(t, base, "transfers", "wait", id)
	artifact["wait"] = w
	if w.Code != 0 || !strings.Contains(w.Stdout, "Copied big.bin") {
		t.Fatalf("ASSERTION: transfers wait after resume exited %d: %s %s", w.Code, w.Stdout, w.Stderr)
	}
	if fileSHA(t, filepath.Join(here, "big.bin")) != want {
		t.Fatalf("ASSERTION: the resumed copy is not the original")
	}

	r = runTuios(t, base, "cp", "--detach", "build:big.bin", filepath.Join(here, "big-two.bin"))
	id = strings.TrimSpace(r.Stdout)
	time.Sleep(time.Second)
	if c := runTuios(t, base, "transfers", "cancel", id); c.Code != 0 {
		t.Fatalf("transfers cancel exited %d: %s", c.Code, c.Stderr)
	}
	w = runTuios(t, base, "transfers", "wait", "--json", id)
	artifact["wait_cancelled"] = w
	if w.Code != 130 {
		t.Fatalf("ASSERTION: transfers wait on a cancelled copy exited %d, want 130: %s", w.Code, w.Stdout)
	}
	if _, err := os.Stat(filepath.Join(here, "big-two.bin")); err == nil {
		t.Fatalf("ASSERTION: the cancelled copy is in place")
	}
	if parts, _ := filepath.Glob(filepath.Join(here, ".big-two.bin.tuios-part*")); len(parts) > 0 {
		t.Fatalf("ASSERTION: the cancelled copy left its part: %v", parts)
	}
	if c := runTuios(t, base, "transfers", "clear"); c.Code != 0 || !strings.Contains(c.Stdout, "2 copies") {
		t.Fatalf("ASSERTION: transfers clear: %d %s", c.Code, c.Stdout)
	}
	saveTransferArtifact(t, "cp-pause-resume-cancel", artifact)
}
