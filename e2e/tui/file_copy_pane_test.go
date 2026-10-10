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
	if len(listing.Transfers) != 1 || listing.Transfers[0].Name != "report.txt" || listing.Transfers[0].Pane != pane {
		t.Fatalf("ASSERTION: the pane sees %+v, want its one copy, tagged with %s", listing.Transfers, pane)
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
	screens["ask"] = waitPaneScreen(t, base, "work", pane, 30*time.Second, "is there on build", "Replace it, keep both, or skip?")
	pressInPane(t, base, "work", pane, "K")
	if c := waitExitFile(t, codeFile, 30*time.Second); c != 0 {
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
	screens["progress"] = waitPaneScreen(t, base, "work", pane, 30*time.Second, "big.bin", "to "+cwd, "MiB of 24.0 MiB", "[#", "left")
	pressInPane(t, base, "work", pane, "ctrl+c")
	screens["ctrl_c"] = waitPaneScreen(t, base, "work", pane, 10*time.Second, "Cancel the copy, or keep it running? [c/k]")
	pressInPane(t, base, "work", pane, "k")
	if c := waitExitFile(t, codeFile, 10*time.Second); c != 0 {
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
