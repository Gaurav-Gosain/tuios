package tuie2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// tuios cp in a pane: the terminal a person sees, and the grants a pane holds.
//
// A pane is a real terminal, so the progress block, the conflict question and
// the Ctrl+C question are what a person in that pane gets. The commands are
// typed into the pane with send-text, the keys pressed with send-keys, and the
// screen read with capture-pane; each test saves the screens it read.

// copyPane makes a detached session on the hub and returns its pane's id and
// working folder.
func copyTestPane(t *testing.T, base, session string) (string, string) {
	t.Helper()
	if out, err := tuiosCLI(t, base, "new", session, "--detach"); err != nil {
		t.Fatalf("make session %s: %v\n%s", session, err, out)
	}
	out, err := tuiosCLI(t, base, "list-windows", "-s", session, "--json")
	if err != nil {
		t.Fatalf("list-windows: %v\n%s", err, out)
	}
	var listing struct {
		Windows []struct {
			WindowID string `json:"window_id"`
			Cwd      string `json:"cwd"`
		} `json:"windows"`
	}
	if err := json.Unmarshal([]byte(out), &listing); err != nil || len(listing.Windows) != 1 {
		t.Fatalf("list-windows gave no single window: %v\n%s", err, out)
	}
	w := listing.Windows[0]
	if w.Cwd == "" {
		w.Cwd = workDirIn(t, base)
	}
	return w.WindowID, w.Cwd
}

// typeLine types a line into the pane and presses Enter.
func typeInPane(t *testing.T, base, session, pane, line string) {
	t.Helper()
	if out, err := tuiosCLI(t, base, "send-text", "-s", session, "-w", pane, line+"\n"); err != nil {
		t.Fatalf("send-text: %v\n%s", err, out)
	}
}

func pressInPane(t *testing.T, base, session, pane, keys string) {
	t.Helper()
	if out, err := tuiosCLI(t, base, "send-keys", "-s", session, "-w", pane, keys); err != nil {
		t.Fatalf("send-keys %s: %v\n%s", keys, err, out)
	}
}

func paneScreen(t *testing.T, base, session, pane string) string {
	t.Helper()
	out, _ := tuiosCLI(t, base, "capture-pane", "-s", session, "-w", pane)
	return out
}

// waitScreen waits until the pane's screen holds every marker, and returns it.
func waitPaneScreen(t *testing.T, base, session, pane string, timeout time.Duration, markers ...string) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var s string
	for time.Now().Before(deadline) {
		s = paneScreen(t, base, session, pane)
		ok := true
		for _, m := range markers {
			if !strings.Contains(s, m) {
				ok = false
				break
			}
		}
		if ok {
			return s
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("ASSERTION: the pane never showed %q. The screen:\n%s", markers, s)
	return s
}

// waitExit waits for a file the pane's shell writes an exit code into.
func waitExitFile(t *testing.T, path string, timeout time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil && strings.HasSuffix(string(b), "\n") {
			n, err := strconv.Atoi(strings.TrimSpace(string(b)))
			if err == nil {
				return n
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("ASSERTION: the command in the pane did not end in %v", timeout)
	return -1
}

// waitPaneExit is waitExitFile for a command typed into a pane: on a timeout
// it fails with the pane's screen, which says where the command stopped.
func waitPaneExit(t *testing.T, base, session, pane, path string, timeout time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(path); err == nil && strings.HasSuffix(string(b), "\n") {
			if n, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
				return n
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("ASSERTION: the command in the pane did not end in %v. The screen:\n%s", timeout, paneScreen(t, base, session, pane))
	return -1
}

// TestAStrictPaneCopiesOnlyWithTheFilesGrant runs tuios cp in a pane under
// [agents.permissions] mode strict. On the default grants it is refused with
// exit 4 and nothing reaches build. Given files, it copies a file from its
// own folder to build's home and the copy carries the pane's id, but a file
// outside its folder and a folder outside build's home are still refused
// with exit 4, and it does not see the person's copies.
//
// Negative control: with the files check cut from transferGrantStart, the
// copy from outside the pane's folder exits 0.
func TestAStrictPaneCopiesOnlyWithTheFilesGrant(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	hubWithFileHost(t, base, remote, writeFakeSSHTo(t, base, remote))
	cfg := configPathIn(base)
	body, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, append(body, []byte("\n[agents.permissions]\nmode = \"strict\"\n")...), 0o600); err != nil {
		t.Fatal(err)
	}

	pane, cwd := copyTestPane(t, base, "work")
	inside := filepath.Join(cwd, "report.txt")
	if err := os.WriteFile(inside, []byte("made in the pane's folder\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(base, "secret.txt")
	if err := os.WriteFile(outside, []byte("not the pane's\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A copy of the person's, which the pane must not see.
	if r := runTuios(t, base, "cp", "--detach", outside, "build:persons.txt"); r.Code != 0 {
		t.Fatalf("the person's copy did not start: %s", r.Stderr)
	}
	codes := filepath.Join(base, "codes")
	if err := os.MkdirAll(codes, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(name, args string) int {
		t.Helper()
		out := filepath.Join(codes, name)
		// A run of the same name before left its files; a stale code would
		// end the wait before this run, and the next one truncate its
		// stderr.
		_ = os.Remove(out)
		_ = os.Remove(out + ".err")
		typeInPane(t, base, "work", pane, tuiosBin+" cp --conflict replace "+args+" 2>"+out+".err; echo $? >"+out)
		return waitExitFile(t, out, 60*time.Second)
	}
	results := map[string]any{}

	// The strict default reloads from the config file; wait for it to hold.
	deadline := time.Now().Add(20 * time.Second)
	code := run("default", "./report.txt build:")
	for code != 4 && time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		_ = os.Remove(filepath.Join(farHome(remote), "report.txt"))
		code = run("default", "./report.txt build:")
	}
	errText, _ := os.ReadFile(filepath.Join(codes, "default.err"))
	results["default"] = map[string]any{"code": code, "stderr": string(errText)}
	if code != 4 || !strings.Contains(string(errText), "files grant") {
		t.Fatalf("ASSERTION: a strict pane without files copied, or was refused without naming the grant: exit %d\n%s", code, errText)
	}
	if _, err := os.Stat(filepath.Join(farHome(remote), "report.txt")); err == nil {
		t.Fatalf("ASSERTION: the refused copy reached build")
	}

	if out, err := tuiosCLI(t, base, "set-pane-grants", "-s", "work", "-w", pane, "--grants", "read,files"); err != nil {
		t.Fatalf("set-pane-grants: %v\n%s", err, out)
	}
	code = run("granted", "./report.txt build:")
	results["granted"] = code
	if code != 0 {
		errText, _ = os.ReadFile(filepath.Join(codes, "granted.err"))
		t.Fatalf("ASSERTION: a pane with files could not copy from its folder: exit %d\n%s", code, errText)
	}
	if got, _ := os.ReadFile(filepath.Join(farHome(remote), "report.txt")); string(got) != "made in the pane's folder\n" {
		t.Fatalf("ASSERTION: the pane's copy is not on build: %q", got)
	}
	code = run("outside", outside+" build:")
	errText, _ = os.ReadFile(filepath.Join(codes, "outside.err"))
	results["outside"] = map[string]any{"code": code, "stderr": string(errText)}
	if code != 4 || !strings.Contains(string(errText), "outside the pane's folder") {
		t.Fatalf("ASSERTION: a pane with files copied a file outside its folder: exit %d\n%s", code, errText)
	}
	code = run("far-root", "./report.txt build:/tmp/")
	results["far_root"] = code
	if code != 4 {
		t.Fatalf("ASSERTION: a pane with files copied outside build's home: exit %d", code)
	}

	// What the pane sees of the copies: its own, with its id, and not the
	// person's.
	listOut := filepath.Join(codes, "list.json")
	typeInPane(t, base, "work", pane, tuiosBin+" transfers --json >"+listOut+"; echo $? >"+listOut+".code")
	if c := waitExitFile(t, listOut+".code", 30*time.Second); c != 0 {
		t.Fatalf("ASSERTION: tuios transfers in the pane exited %d", c)
	}
	raw, _ := os.ReadFile(listOut)
	var listing struct {
		Transfers []struct {
			Name string `json:"name"`
			Pane string `json:"pane"`
		} `json:"transfers"`
	}
	if err := json.Unmarshal(raw, &listing); err != nil {
		t.Fatalf("transfers --json: %v\n%s", err, raw)
	}
	results["pane_list"] = listing.Transfers
	// The pane's copies are the ones it ran before strict mode held and the
	// one with the grant: each is report.txt with its id. The person's copy
	// of persons.txt is not among them.
	if len(listing.Transfers) == 0 {
		t.Fatalf("ASSERTION: the pane sees none of its own copies")
	}
	for _, tr := range listing.Transfers {
		if tr.Name != "report.txt" || tr.Pane != pane {
			t.Fatalf("ASSERTION: the pane sees %+v, want only its own copies, tagged with %s", listing.Transfers, pane)
		}
	}
	saveTransferArtifact(t, "cp-strict-pane", results)
}

// TestCopyOnATerminalAsksAndKeepsRunning runs tuios cp in a pane, which is a
// terminal. A folder onto one that differs asks about the files; K answers
// keep both for all of them. A slow copy draws its progress block in place,
// and Ctrl+C then k leaves the copy running in the daemon, where it ends
// checked.
func TestCopyOnATerminalAsksAndKeepsRunning(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	hubWithFileHost(t, base, remote, writeSlowFakeSSH(t, base, remote, 2<<20, 1<<20))
	host, _ := os.Hostname()
	host, _, _ = strings.Cut(host, ".")
	pane, cwd := copyTestPane(t, base, "work")
	screens := map[string]string{}
	defer func() { saveTransferArtifact(t, "cp-terminal", screens) }()

	// The question about files that differ.
	src := filepath.Join(cwd, "docs")
	for i, name := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.MkdirAll(src, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(src, name), []byte(strings.Repeat("new ", i+1)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	far := filepath.Join(farHome(remote), "docs")
	if err := os.MkdirAll(far, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(far, name), []byte("old on build"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	codeFile := filepath.Join(base, "ask.code")
	typeInPane(t, base, "work", pane, "clear; "+tuiosBin+" cp ./docs build:; echo $? >"+codeFile)
	screens["ask"] = waitPaneScreen(t, base, "work", pane, 30*time.Second, "a.txt", "Replace it, keep both, or skip?")
	pressInPane(t, base, "work", pane, "K")
	if c := waitPaneExit(t, base, "work", pane, codeFile, 30*time.Second); c != 0 {
		t.Fatalf("ASSERTION: cp after keep both for all exited %d\n%s", c, paneScreen(t, base, "work", pane))
	}
	screens["ask_done"] = paneScreen(t, base, "work", pane)
	for _, name := range []string{"a", "b"} {
		old, _ := os.ReadFile(filepath.Join(far, name+".txt"))
		kept, err := os.ReadFile(filepath.Join(far, name+" (from "+host+").txt"))
		if string(old) != "old on build" || err != nil {
			t.Fatalf("ASSERTION: keep both did not keep %s.txt and add the new one beside it: %q %v", name, old, err)
		}
		if len(kept) == 0 {
			t.Fatalf("ASSERTION: the kept copy of %s is empty", name)
		}
	}

	// The progress block, then Ctrl+C and keep.
	big := filepath.Join(farHome(remote), "big.bin")
	want := randomFile(t, big, 24<<20)
	codeFile = filepath.Join(base, "keep.code")
	typeInPane(t, base, "work", pane, "clear; "+tuiosBin+" cp build:big.bin ./; echo $? >"+codeFile)
	screens["progress"] = waitPaneScreen(t, base, "work", pane, 30*time.Second, "big.bin", "  to /", "MiB of 24.0 MiB", "[#", "left")
	pressInPane(t, base, "work", pane, "ctrl+c")
	screens["ctrl_c"] = waitPaneScreen(t, base, "work", pane, 10*time.Second, "Cancel the copy, or keep it running? [c/k]")
	pressInPane(t, base, "work", pane, "k")
	if c := waitPaneExit(t, base, "work", pane, codeFile, 10*time.Second); c != 0 {
		t.Fatalf("ASSERTION: cp after Ctrl+C and k exited %d", c)
	}
	screens["kept"] = waitPaneScreen(t, base, "work", pane, 5*time.Second, "The copy goes on in the daemon")
	w := runTuios(t, base, "transfers", "wait")
	if w.Code != 0 {
		t.Fatalf("ASSERTION: the kept copy did not end well: %d %s %s", w.Code, w.Stdout, w.Stderr)
	}
	if fileSHA(t, filepath.Join(cwd, "big.bin")) != want {
		t.Fatalf("ASSERTION: the kept copy is not the original")
	}
}

// TestAFilesGrantCopyCannotWriteThroughALink gives a strict pane the files
// grant, and puts a link in its folder that points out of it. A copy from
// build into the folder that would land through the link is refused with
// exit 4, and nothing reaches the folder the link points at. A copy that
// stays inside the folder still lands.
//
// Negative control: with the job's os.Root at the pane's folder cut
// (PaneRoot left empty in transferGrantStart), the host's file lands in the
// folder outside.
func TestAFilesGrantCopyCannotWriteThroughALink(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	hubWithFileHost(t, base, remote, writeFakeSSHTo(t, base, remote))
	cfg := configPathIn(base)
	body, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, append(body, []byte("\n[agents.permissions]\nmode = \"strict\"\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	pane, cwd := copyTestPane(t, base, "work")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(cwd, "payload")); err != nil {
		t.Fatal(err)
	}
	far := filepath.Join(farHome(remote), "payload")
	if err := os.MkdirAll(far, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(far, "dropped.sh"), []byte("echo from build\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(farHome(remote), "fine.txt"), []byte("fine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	codes := filepath.Join(base, "codes")
	if err := os.MkdirAll(codes, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(name, args string) (int, string) {
		t.Helper()
		out := filepath.Join(codes, name)
		_ = os.Remove(out)
		_ = os.Remove(out + ".err")
		typeInPane(t, base, "work", pane, tuiosBin+" cp --conflict replace "+args+" 2>"+out+".err; echo $? >"+out)
		code := waitPaneExit(t, base, "work", pane, out, 60*time.Second)
		errText, _ := os.ReadFile(out + ".err")
		return code, string(errText)
	}
	results := map[string]any{}
	// Strict mode reloads from the config file. Wait until a pane without
	// the files grant is refused, so the copies below run under strict.
	deadline := time.Now().Add(20 * time.Second)
	code, errText := run("strict", "build:~/fine.txt ./")
	for code != 4 && time.Now().Before(deadline) {
		_ = os.Remove(filepath.Join(cwd, "fine.txt"))
		time.Sleep(500 * time.Millisecond)
		code, errText = run("strict", "build:~/fine.txt ./")
	}
	if code != 4 {
		t.Fatalf("ASSERTION: strict mode never held for the pane: exit %d\n%s", code, errText)
	}
	_ = os.Remove(filepath.Join(cwd, "fine.txt"))
	if out, err := tuiosCLI(t, base, "set-pane-grants", "-s", "work", "-w", pane, "--grants", "read,files"); err != nil {
		t.Fatalf("set-pane-grants: %v\n%s", err, out)
	}
	code, errText = run("inside", "build:~/fine.txt ./")
	results["inside"] = map[string]any{"code": code, "stderr": errText}
	if code != 0 {
		t.Fatalf("ASSERTION: a copy inside the pane's folder failed: exit %d\n%s", code, errText)
	}
	if got, _ := os.ReadFile(filepath.Join(cwd, "fine.txt")); string(got) != "fine\n" {
		t.Fatalf("ASSERTION: the copy inside the folder did not land: %q", got)
	}
	code, errText = run("through-link", "build:~/payload ./")
	results["through_link"] = map[string]any{"code": code, "stderr": errText}
	saveTransferArtifact(t, "cp-files-grant-link", results)
	if _, err := os.Stat(filepath.Join(outside, "dropped.sh")); err == nil {
		t.Fatalf("ASSERTION: a files-grant copy wrote through a link out of the pane's folder (exit %d)\n%s", code, errText)
	}
	if code == 0 {
		t.Fatalf("ASSERTION: a copy that could not land through the link exited 0\n%s", errText)
	}
}

// TestAFilesGrantFolderIsTheOneThePaneStartedIn gives a strict pane the files
// grant and tries to copy a file from outside its folder to build three ways:
// from the folder it started in, after cd to a folder above, and after
// printing an OSC 7 that names /. Each is refused with exit 4 and nothing
// reaches build. A copy from the start folder still lands.
//
// Negative control: with paneFolder taking the pane's working folder (the
// old paneRoot), the copy after cd exits 0 and the file lands on build.
func TestAFilesGrantFolderIsTheOneThePaneStartedIn(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	hubWithFileHost(t, base, remote, writeFakeSSHTo(t, base, remote))
	cfg := configPathIn(base)
	body, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, append(body, []byte("\n[agents.permissions]\nmode = \"strict\"\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	pane, cwd := copyTestPane(t, base, "work")
	secret := filepath.Join(base, "secret.txt")
	if err := os.WriteFile(secret, []byte("not the pane's\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "mine.txt"), []byte("the pane's\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	codes := filepath.Join(base, "codes")
	if err := os.MkdirAll(codes, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(name, line string) (int, string) {
		t.Helper()
		out := filepath.Join(codes, name)
		_ = os.Remove(out)
		_ = os.Remove(out + ".err")
		typeInPane(t, base, "work", pane, line+" 2>"+out+".err; echo $? >"+out)
		code := waitPaneExit(t, base, "work", pane, out, 60*time.Second)
		errText, _ := os.ReadFile(out + ".err")
		return code, string(errText)
	}
	cp := tuiosBin + " cp --conflict replace "
	// Strict mode reloads from the config file: wait until the pane, with no
	// files grant yet, is refused.
	deadline := time.Now().Add(20 * time.Second)
	code, errText := run("strict", cp+filepath.Join(cwd, "mine.txt")+" build:~/strict.txt")
	for code != 4 && time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		code, errText = run("strict", cp+filepath.Join(cwd, "mine.txt")+" build:~/strict.txt")
	}
	if code != 4 {
		t.Fatalf("ASSERTION: strict mode never held for the pane: exit %d\n%s", code, errText)
	}
	if out, err := tuiosCLI(t, base, "set-pane-grants", "-s", "work", "-w", pane, "--grants", "read,files"); err != nil {
		t.Fatalf("set-pane-grants: %v\n%s", err, out)
	}
	results := map[string]any{}
	code, errText = run("start", cp+secret+" build:~/from-start.txt")
	results["start"] = map[string]any{"code": code, "stderr": errText}
	code2, errText2 := run("after-cd", "cd "+base+" && "+cp+secret+" build:~/after-cd.txt")
	results["after_cd"] = map[string]any{"code": code2, "stderr": errText2}
	code3, errText3 := run("after-osc7", "printf '\\033]7;file://localhost/\\007'; "+cp+secret+" build:~/after-osc7.txt")
	results["after_osc7"] = map[string]any{"code": code3, "stderr": errText3}
	code4, errText4 := run("inside", "cd "+cwd+" && "+cp+"./mine.txt build:~/mine.txt")
	results["inside"] = map[string]any{"code": code4, "stderr": errText4}
	saveTransferArtifact(t, "cp-files-grant-folder", results)
	for _, c := range []struct {
		name, file, stderr string
		code               int
	}{{"from the start folder", "from-start.txt", errText, code}, {"after cd", "after-cd.txt", errText2, code2}, {"after an OSC 7 of /", "after-osc7.txt", errText3, code3}} {
		if c.code != 4 {
			t.Errorf("ASSERTION: a copy of a file outside the pane's folder %s exited %d, want 4\n%s", c.name, c.code, c.stderr)
		}
		if _, err := os.Stat(filepath.Join(farHome(remote), c.file)); err == nil {
			t.Errorf("ASSERTION: the copy %s reached build", c.name)
		}
	}
	if code4 != 0 {
		t.Fatalf("ASSERTION: a copy from the pane's own folder failed: exit %d\n%s", code4, errText4)
	}
	if got, _ := os.ReadFile(filepath.Join(farHome(remote), "mine.txt")); string(got) != "the pane's\n" {
		t.Fatalf("ASSERTION: the copy from the pane's folder is not on build: %q", got)
	}
}

// TestAFilesGrantFolderHoldsThroughOtherPanes starts a strict pane with read,
// write, fan and files in a project folder under base, and a file in base
// itself that it must not reach. It tries to reach the file through other
// panes:
//
//   - a copy from the pane itself (refused, exit 4);
//   - start-agent --cwd base (refused before anything starts);
//   - cd base, then start-agent with no --cwd: the agent starts in the
//     pane's folder, and its copy of the file is refused;
//   - typing a copy into a sibling pane that holds files for base (refused).
//
// An agent started in a folder inside the pane's folder copies a file there
// to build. A pane whose start folder is the home folder is refused, and the
// message says to start the pane in a project folder.
//
// An agent started in a link inside the folder keeps the folder the link
// named then, after the link is repointed to the parent, and a pane started
// in /etc, which another user owns, is refused.
//
// Negative control: with addWindowFor not holding the window to the
// caller's folder (Bounded left false), the agent started after cd base
// copies the file to build.
func TestAFilesGrantFolderHoldsThroughOtherPanes(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	hubWithFileHost(t, base, remote, writeFakeSSHTo(t, base, remote))
	cfg := configPathIn(base)
	body, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg, append(body, []byte("\n[agents.permissions]\nmode = \"strict\"\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _ = copyTestPane(t, base, "work")
	// The pane works in base/projects/app. Its parent, base/projects, holds
	// no home folder, so a pane started there would hold files for it.
	parent := filepath.Join(base, "projects")
	cwd := filepath.Join(parent, "app")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	pane := personWindow(t, base, cwd)
	secret := filepath.Join(parent, "secret.txt")
	if err := os.WriteFile(secret, []byte("not the pane's\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(cwd, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "inner.txt"), []byte("inside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	codes := filepath.Join(base, "codes")
	if err := os.MkdirAll(codes, 0o755); err != nil {
		t.Fatal(err)
	}
	// agentScript writes a script an agent pane runs: one copy, and its exit
	// code in a file.
	agentScript := func(name, src, dst string) (string, string) {
		t.Helper()
		out := filepath.Join(codes, name)
		script := filepath.Join(codes, name+".sh")
		body := "#!/bin/sh\n" + tuiosBin + " cp --conflict replace " + src + " " + dst + " 2>" + out + ".err; echo $? >" + out + "\nexec sleep 600\n"
		if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		return script, out
	}
	run := func(name, line string) (int, string) {
		t.Helper()
		out := filepath.Join(codes, name)
		_ = os.Remove(out)
		_ = os.Remove(out + ".err")
		typeInPane(t, base, "work", pane, line+" 2>"+out+".err; echo $? >"+out)
		code := waitPaneExit(t, base, "work", pane, out, 90*time.Second)
		errText, _ := os.ReadFile(out + ".err")
		return code, string(errText)
	}
	cp := tuiosBin + " cp --conflict replace "
	deadline := time.Now().Add(20 * time.Second)
	code, errText := run("strict", cp+filepath.Join(sub, "inner.txt")+" build:~/strict.txt")
	for code != 4 && time.Now().Before(deadline) {
		time.Sleep(500 * time.Millisecond)
		code, errText = run("strict", cp+filepath.Join(sub, "inner.txt")+" build:~/strict.txt")
	}
	if code != 4 {
		t.Fatalf("ASSERTION: strict mode never held for the pane: exit %d\n%s", code, errText)
	}
	if out, err := tuiosCLI(t, base, "set-pane-grants", "-s", "work", "-w", pane, "--grants", "read,write,fan,files"); err != nil {
		t.Fatalf("set-pane-grants: %v\n%s", err, out)
	}
	results := map[string]any{}
	far := farHome(remote)
	refused := func(row, file string, code int, errText string) {
		t.Helper()
		results[row] = map[string]any{"code": code, "stderr": errText}
		if code == 0 {
			t.Errorf("ASSERTION: %s exited 0\n%s", row, errText)
		}
		if file != "" {
			if _, err := os.Stat(filepath.Join(far, file)); err == nil {
				t.Errorf("ASSERTION: %s reached build (%s)", row, file)
			}
		}
	}
	waitFile := func(path string) (int, string) {
		t.Helper()
		end := time.Now().Add(60 * time.Second)
		for time.Now().Before(end) {
			if b, err := os.ReadFile(path); err == nil && strings.HasSuffix(string(b), "\n") {
				n, _ := strconv.Atoi(strings.TrimSpace(string(b)))
				e, _ := os.ReadFile(path + ".err")
				return n, string(e)
			}
			time.Sleep(200 * time.Millisecond)
		}
		t.Fatalf("ASSERTION: the agent never wrote %s", path)
		return -1, ""
	}

	// 1. The pane itself.
	code, errText = run("direct", cp+secret+" build:~/direct.txt")
	refused("a direct copy from outside the folder", "direct.txt", code, errText)

	// 2. start-agent --cwd parent.
	script, out := agentScript("cwd-parent", secret, "build:~/cwd-parent.txt")
	code, errText = run("cwd-parent-start", tuiosBin+" start-agent --ready-timeout 500 --cwd "+parent+" "+script)
	results["start-agent --cwd parent"] = map[string]any{"code": code, "stderr": errText}
	if code == 0 || !strings.Contains(errText, "outside the folder") {
		t.Errorf("ASSERTION: start-agent --cwd outside the pane's folder was not refused: exit %d\n%s", code, errText)
	}
	time.Sleep(time.Second)
	if _, err := os.Stat(out); err == nil {
		t.Errorf("ASSERTION: an agent started with --cwd outside the folder ran")
	}

	// 3. cd parent, then start-agent with no --cwd.
	script, out = agentScript("cd-parent", secret, "build:~/cd-parent.txt")
	_, _ = run("cd-parent-start", "cd "+parent+" && "+tuiosBin+" start-agent --ready-timeout 500 "+script+"; cd "+cwd)
	code, errText = waitFile(out)
	refused("an agent started after cd to the parent", "cd-parent.txt", code, errText)

	// 4. Typing into a sibling with a wider folder.
	sibID := personWindow(t, base, parent)
	if out, err := tuiosCLI(t, base, "set-pane-grants", "-s", "work", "-w", sibID, "--grants", "read,write,fan,files"); err != nil {
		t.Fatalf("set-pane-grants on the sibling: %v\n%s", err, out)
	}
	code, errText = run("type-sibling", tuiosBin+" send-text -s work -w "+sibID+" '"+cp+secret+" build:~/typed.txt\n'")
	refused("typing a copy into a sibling with a wider folder", "", code, errText)
	time.Sleep(2 * time.Second)
	if _, err := os.Stat(filepath.Join(far, "typed.txt")); err == nil {
		t.Errorf("ASSERTION: the text typed into the sibling copied the file to build")
	}

	// 5. An agent inside the folder copies within it.
	script, out = agentScript("inside", filepath.Join(sub, "inner.txt"), "build:~/inner.txt")
	_, _ = run("inside-start", tuiosBin+" start-agent --ready-timeout 500 --cwd "+sub+" "+script)
	code, errText = waitFile(out)
	results["an agent inside the folder"] = map[string]any{"code": code, "stderr": errText}
	if code != 0 {
		t.Errorf("ASSERTION: an agent started inside the pane's folder could not copy a file there: exit %d\n%s", code, errText)
	} else if got, _ := os.ReadFile(filepath.Join(far, "inner.txt")); string(got) != "inside\n" {
		t.Errorf("ASSERTION: the agent's copy is not on build: %q", got)
	}

	// 6. A pane whose start folder is the home folder.
	home := xdgDir(base, "HOME")
	homeID := personWindow(t, base, home)
	if out, err := tuiosCLI(t, base, "set-pane-grants", "-s", "work", "-w", homeID, "--grants", "read,files"); err != nil {
		t.Fatalf("set-pane-grants on the home pane: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(home, "notes.txt"), []byte("home\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	homeCode := filepath.Join(codes, "home")
	typeInPane(t, base, "work", homeID, cp+filepath.Join(home, "notes.txt")+" build:~/home.txt 2>"+homeCode+".err; echo $? >"+homeCode)
	code = waitPaneExit(t, base, "work", homeID, homeCode, 60*time.Second)
	he, _ := os.ReadFile(homeCode + ".err")
	refused("a copy from a pane started in the home folder", "home.txt", code, string(he))
	if !strings.Contains(string(he), "project folder") {
		t.Errorf("ASSERTION: the refusal does not say to start the pane in a project folder:\n%s", he)
	}

	// 7. A link in the folder an agent started in, repointed after it
	// started, does not move the agent's folder.
	sub2 := filepath.Join(cwd, "sub2")
	if err := os.MkdirAll(sub2, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(cwd, "link")
	if err := os.Symlink(sub2, link); err != nil {
		t.Fatal(err)
	}
	trigger := filepath.Join(codes, "repoint.go")
	out = filepath.Join(codes, "repoint")
	script = filepath.Join(codes, "repoint.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nwhile [ ! -e "+trigger+" ]; do sleep 0.2; done\n"+cp+secret+" build:~/repoint.txt 2>"+out+".err; echo $? >"+out+"\nexec sleep 600\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	code, errText = run("repoint-start", tuiosBin+" start-agent --ready-timeout 500 --cwd "+link+" "+script)
	results["start-agent in a link"] = map[string]any{"code": code, "stderr": errText}
	time.Sleep(2 * time.Second)
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(parent, link); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(trigger, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	code, errText = waitFile(out)
	refused("an agent whose start link was repointed to the parent", "repoint.txt", code, errText)

	// 8. A pane started in a folder another user owns.
	sysID := personWindow(t, base, "/etc")
	if out, err := tuiosCLI(t, base, "set-pane-grants", "-s", "work", "-w", sysID, "--grants", "read,files"); err != nil {
		t.Fatalf("set-pane-grants on the /etc pane: %v\n%s", err, out)
	}
	sysCode := filepath.Join(codes, "etc")
	typeInPane(t, base, "work", sysID, cp+"/etc/passwd build:~/etc.txt 2>"+sysCode+".err; echo $? >"+sysCode)
	code = waitPaneExit(t, base, "work", sysID, sysCode, 60*time.Second)
	se, _ := os.ReadFile(sysCode + ".err")
	refused("a copy from a pane started in /etc", "etc.txt", code, string(se))
	saveTransferArtifact(t, "cp-files-grant-other-panes", results)
}

// personWindow opens a window in session work with the person's CLI, started
// in dir, and returns its id.
func personWindow(t *testing.T, base, dir string) string {
	t.Helper()
	out, err := tuiosCLI(t, base, "new-window", "-s", "work", "--cwd", dir, "--json")
	if err != nil {
		t.Fatalf("new-window in %s: %v\n%s", dir, err, out)
	}
	var w struct {
		WindowID string `json:"window_id"`
		ID       string `json:"id"`
	}
	_ = json.Unmarshal([]byte(out), &w)
	if w.WindowID != "" {
		return w.WindowID
	}
	if w.ID == "" {
		t.Fatalf("new-window gave no window id: %s", out)
	}
	return w.ID
}
