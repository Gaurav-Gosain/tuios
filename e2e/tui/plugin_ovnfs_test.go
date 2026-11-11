package tuie2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// plugins/ovnfs, the shipped folder. With no ovnfs on PATH an action says
// how to install it and does nothing else. With an ovnfs older than 0.4.0 it
// says to update and runs nothing but ovnfs version: from 0.3.0 on ovnfs rm
// deletes a checkout's snapshots unless it gets --sticky-snapshots, which
// older ones refuse. With TUIOS_E2E_OVNFS naming an ovnfs binary (0.4.0 or
// newer), the test runs it for real, in a daemon of its own under the test's
// folder:
//
//   - New checkout here opens a checkout of the focused pane's folder in a
//     new pane, and writes nothing to the folder.
//   - Closing that pane with changes says so and sets the cell. It never
//     applies.
//   - Apply asks for a typed yes, then merges the changes into the folder.
//   - Removing a worktree that tuios made removes its checkout, and keeps the
//     checkout's changes in a snapshot first: ovnfs new --from the snapshot
//     opens them again. This needs the worktree-removed event (a build
//     without it leaves the checkout), and --sticky-snapshots on the rm (on
//     ovnfs 0.3.0 and newer the rm deletes the snapshot without it).
//
// ovnfs needs an NFS mount on macOS and FUSE on Linux, which CI does not
// have, so the second part runs where a person sets the variable.
func TestPluginOvnfs(t *testing.T) {
	plugin, err := filepath.Abs(filepath.Join("..", "..", "plugins", "ovnfs"))
	if err != nil {
		t.Fatal(err)
	}
	art := artifactDir(t)

	t.Run("not installed", func(t *testing.T) {
		// PATH has the system tools and jq, and no ovnfs.
		bin := t.TempDir()
		for _, tool := range []string{"jq"} {
			if p, err := exec.LookPath(tool); err == nil {
				_ = os.Symlink(p, filepath.Join(bin, tool))
			}
		}
		t.Setenv("PATH", bin+string(os.PathListSeparator)+agentFreePath("/usr/bin", "/bin"))
		base := t.TempDir()
		writeConfig(t, base, "")
		killDaemon(t, base)
		if out, err := tuiosCLI(t, base, "new", "ov", "--detach"); err != nil {
			t.Fatalf("create session: %v\n%s", err, out)
		}
		if out, err := tuiosCLI(t, base, "plugins", "install", plugin, "--yes"); err != nil {
			t.Fatalf("install: %v\n%s", err, out)
		}
		term := startIn(t, base, startOpts{args: []string{"attach", "ov"}})
		if err := term.WaitForText("$", bootTimeout); err != nil {
			t.Fatalf("attach: %v\n%s", err, term.Snapshot())
		}
		if out, err := tuiosCLI(t, base, "plugins", "open", "tuios.ovnfs", "new"); err != nil {
			t.Fatalf("open new: %v\n%s", err, out)
		}
		if err := term.WaitForText("needs ovnfs on PATH", uiTimeout); err != nil {
			t.Fatalf("no message about the missing ovnfs: %v\n%s", err, term.Snapshot())
		}
		savePNG(t, term.Screen(), hostPalette(t, ""), art, "ovnfs-not-installed")
		if out, _ := tuiosCLI(t, base, "plugins", "log", "tuios.ovnfs"); strings.Contains(out, "ovnfs new") {
			t.Errorf("the pane ran ovnfs with no ovnfs:\n%s", out)
		}
	})

	t.Run("too old", func(t *testing.T) {
		// An ovnfs that answers version with 0.2.3 and writes down every
		// call, and jq. Nothing else of ovnfs may run.
		bin := t.TempDir()
		calls := filepath.Join(bin, "calls")
		fake := "#!/bin/sh\necho \"$*\" >>" + calls + "\n" +
			"case \"$*\" in *version*) echo '{\"version\":\"v0.2.3\",\"go\":\"go1.27\"}' ;; *) exit 3 ;; esac\n"
		if err := os.WriteFile(filepath.Join(bin, "ovnfs"), []byte(fake), 0o755); err != nil {
			t.Fatal(err)
		}
		if p, err := exec.LookPath("jq"); err == nil {
			_ = os.Symlink(p, filepath.Join(bin, "jq"))
		}
		t.Setenv("PATH", bin+string(os.PathListSeparator)+agentFreePath("/usr/bin", "/bin"))
		base := t.TempDir()
		writeConfig(t, base, "")
		killDaemon(t, base)
		if out, err := tuiosCLI(t, base, "new", "ov", "--detach"); err != nil {
			t.Fatalf("create session: %v\n%s", err, out)
		}
		if out, err := tuiosCLI(t, base, "plugins", "install", plugin, "--yes"); err != nil {
			t.Fatalf("install: %v\n%s", err, out)
		}
		term := startIn(t, base, startOpts{args: []string{"attach", "ov"}})
		if err := term.WaitForText("$", bootTimeout); err != nil {
			t.Fatalf("attach: %v\n%s", err, term.Snapshot())
		}
		if out, err := tuiosCLI(t, base, "plugins", "open", "tuios.ovnfs", "new"); err != nil {
			t.Fatalf("open new: %v\n%s", err, out)
		}
		if err := term.WaitForText("needs ovnfs 0.4.0 or newer", uiTimeout); err != nil {
			t.Fatalf("ASSERTION: no message about the old ovnfs: %v\n%s", err, term.Snapshot())
		}
		savePNG(t, term.Screen(), hostPalette(t, ""), art, "ovnfs-too-old")
		b, _ := os.ReadFile(calls)
		for line := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
			if !strings.Contains(line, "version") {
				t.Errorf("ASSERTION: the plugin ran ovnfs %q on an ovnfs older than 0.4.0", line)
			}
		}
	})

	ovnfs := os.Getenv("TUIOS_E2E_OVNFS")
	if ovnfs == "" {
		t.Log("TUIOS_E2E_OVNFS is not set: the ovnfs part is skipped")
		return
	}
	t.Run("with ovnfs", func(t *testing.T) { testPluginOvnfsReal(t, plugin, ovnfs, art) })
}

func testPluginOvnfsReal(t *testing.T, plugin, ovnfs, art string) {
	bin := t.TempDir()
	if err := os.Symlink(ovnfs, filepath.Join(bin, "ovnfs")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	// A daemon of the test's own: its state and its mount under the test.
	// The mount path is short, since it goes into NFS handles and mount(8).
	ovdir, err := os.MkdirTemp("", "ovt")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OVNFS_STATE", filepath.Join(ovdir, "state"))
	t.Setenv("OVNFS_MOUNT", filepath.Join(ovdir, "co"))
	ov := func(args ...string) (string, error) {
		out, err := exec.Command("ovnfs", args...).CombinedOutput()
		return string(out), err
	}
	t.Cleanup(func() {
		if out, err := ov("stop"); err != nil {
			t.Logf("ovnfs stop: %v\n%s", err, out)
		}
		_ = os.RemoveAll(ovdir)
	})
	type checkout struct {
		Name, Path, Source string
		Dirty              bool
	}
	type listing struct {
		Checkouts []checkout
		Snapshots []struct{ ID, Checkout, Label string }
	}
	ls := func() listing {
		t.Helper()
		out, err := ov("--json", "ls")
		var l listing
		if err != nil || json.Unmarshal([]byte(out), &l) != nil {
			t.Fatalf("ovnfs ls: %v\n%s", err, out)
		}
		return l
	}
	pathOf := func(name string) string {
		t.Helper()
		for _, c := range ls().Checkouts {
			if c.Name == name {
				return c.Path
			}
		}
		t.Fatalf("no checkout %s", name)
		return ""
	}

	base := t.TempDir()
	writeConfig(t, base, "")
	killDaemon(t, base)
	repo := filepath.Join(base, "demo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	repo, _ = filepath.EvalSymlinks(repo)
	testutil.Git(t, repo, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "notes.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testutil.Git(t, repo, "add", ".")
	testutil.Git(t, repo, "-c", "user.name=e2e", "-c", "user.email=e2e@localhost", "commit", "-q", "-m", "x")
	if out, err := tuiosCLIIn(t, base, repo, "new", "ov", "--detach"); err != nil {
		t.Fatalf("create session: %v\n%s", err, out)
	}
	if out, err := tuiosCLI(t, base, "plugins", "install", plugin, "--yes"); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	term := startIn(t, base, startOpts{args: []string{"attach", "ov"}, cols: 140, rows: 40})
	if err := term.WaitForText("$", bootTimeout); err != nil {
		t.Fatalf("attach: %v\n%s", err, term.Snapshot())
	}

	// New checkout here: a checkout of the folder, in a new pane.
	if out, err := tuiosCLI(t, base, "plugins", "open", "tuios.ovnfs", "new"); err != nil {
		t.Fatalf("open new: %v\n%s", err, out)
	}
	if err := term.WaitForText("This pane works in checkout", uiTimeout); err != nil {
		log, _ := tuiosCLI(t, base, "plugins", "log", "tuios.ovnfs")
		t.Fatalf("no message for the new checkout: %v\n%s\n%s", err, log, term.Snapshot())
	}
	l := ls()
	if len(l.Checkouts) != 1 || l.Checkouts[0].Source != repo {
		t.Fatalf("checkouts after New checkout here: %+v, want one of %s", l.Checkouts, repo)
	}
	co := l.Checkouts[0]
	// The new pane works in the checkout. The mount sits under the temp
	// folder, which macOS reaches through /private, so the end of the path
	// names it.
	pane := ""
	for deadline := time.Now().Add(uiTimeout); pane == "" && time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		out, _ := tuiosCLI(t, base, "list-windows", "-s", "ov", "--json")
		var wins struct {
			Windows []struct {
				ID  string `json:"window_id"`
				Cwd string `json:"cwd"`
			} `json:"windows"`
		}
		_ = json.Unmarshal([]byte(out), &wins)
		for _, w := range wins.Windows {
			if strings.HasSuffix(w.Cwd, "/co/"+co.Name) {
				pane = w.ID
			}
		}
	}
	if pane == "" {
		out, _ := tuiosCLI(t, base, "list-windows", "-s", "ov", "--json")
		t.Fatalf("no pane works in the checkout:\n%s", out)
	}
	savePNG(t, term.Screen(), hostPalette(t, ""), art, "ovnfs-new")

	// An edit in the checkout stays out of the folder.
	if err := os.WriteFile(filepath.Join(co.Path, "notes.txt"), []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "notes.txt")); string(b) != "one\n" {
		t.Fatalf("the folder changed before an apply: %q", b)
	}

	// Close the checkout's pane: a message and the cell, and no apply.
	if out, err := tuiosCLI(t, base, "close-window", "-s", "ov", pane); err != nil {
		t.Fatalf("close the checkout's pane: %v\n%s", err, out)
	}
	if err := term.WaitForText("ovnfs 1 to review", uiTimeout); err != nil {
		log, _ := tuiosCLI(t, base, "plugins", "log", "tuios.ovnfs")
		t.Fatalf("no cell after the pane closed: %v\n%s\n%s", err, log, term.Snapshot())
	}
	savePNG(t, term.Screen(), hostPalette(t, ""), art, "ovnfs-pane-closed")
	time.Sleep(time.Second)
	if b, _ := os.ReadFile(filepath.Join(repo, "notes.txt")); string(b) != "one\n" {
		t.Fatalf("closing the pane applied the changes: %q", b)
	}

	// Apply: the dry run, then a typed yes.
	if out, err := tuiosCLI(t, base, "plugins", "open", "tuios.ovnfs", "apply"); err != nil {
		t.Fatalf("open apply: %v\n%s", err, out)
	}
	if err := term.WaitForText("Type yes and press Enter", uiTimeout); err != nil {
		t.Fatalf("apply did not ask: %v\n%s", err, term.Snapshot())
	}
	if !strings.Contains(term.Snapshot(), "notes.txt") {
		t.Errorf("the dry run does not name the file:\n%s", term.Snapshot())
	}
	savePNG(t, term.Screen(), hostPalette(t, ""), art, "ovnfs-apply-ask")
	// A key that is not yes applies nothing.
	term.Type("y\r")
	if err := term.WaitForText("Nothing was applied", uiTimeout); err != nil {
		t.Fatalf("a bare y did something else: %v\n%s", err, term.Snapshot())
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "notes.txt")); string(b) != "one\n" {
		t.Fatalf("a bare y applied the changes: %q", b)
	}
	term.Type("\r")
	time.Sleep(500 * time.Millisecond)
	if out, err := tuiosCLI(t, base, "plugins", "open", "tuios.ovnfs", "apply"); err != nil {
		t.Fatalf("open apply: %v\n%s", err, out)
	}
	if err := term.WaitForText("Type yes and press Enter", uiTimeout); err != nil {
		t.Fatalf("apply did not ask: %v\n%s", err, term.Snapshot())
	}
	term.Type("yes\r")
	if err := term.WaitForText("as uncommitted edits", uiTimeout); err != nil {
		t.Fatalf("apply did not end: %v\n%s", err, term.Snapshot())
	}
	if b, _ := os.ReadFile(filepath.Join(repo, "notes.txt")); string(b) != "one\ntwo\n" {
		t.Fatalf("after apply the folder holds %q", b)
	}
	term.Type("\r")
	deadline := time.Now().Add(uiTimeout)
	for strings.Contains(term.Snapshot(), "to review") {
		if time.Now().After(deadline) {
			t.Fatalf("the cell stays after the apply:\n%s", term.Snapshot())
		}
		time.Sleep(100 * time.Millisecond)
	}
	testutil.Git(t, repo, "checkout", "-q", "--", ".")
	if out, err := ov("rm", co.Name); err != nil {
		t.Fatalf("ovnfs rm: %v\n%s", err, out)
	}

	// The list of checkouts.
	if out, err := ov("new", repo, "--name", "demo-list"); err != nil {
		t.Fatalf("ovnfs new: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(pathOf("demo-list"), "added.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "plugins", "open", "tuios.ovnfs", "checkouts"); err != nil {
		t.Fatalf("open checkouts: %v\n%s", err, out)
	}
	if err := term.WaitForText("1 added", uiTimeout); err != nil {
		t.Fatalf("the list does not show the change: %v\n%s", err, term.Snapshot())
	}
	savePNG(t, term.Screen(), hostPalette(t, ""), art, "ovnfs-checkouts")
	term.Type("q\r")
	if out, err := ov("rm", "demo-list"); err != nil {
		t.Fatalf("ovnfs rm: %v\n%s", err, out)
	}

	// A worktree that tuios made goes, and its checkout goes with it. Its
	// change stays in a snapshot.
	out, err := tuiosCLIIn(t, base, repo, "worktree", "new", "feat/x", "--json")
	if err != nil {
		t.Fatalf("new-worktree: %v\n%s", err, out)
	}
	var wt struct {
		Session string `json:"session"`
		Path    string `json:"path"`
	}
	if err := json.Unmarshal([]byte(out), &wt); err != nil || wt.Path == "" {
		t.Fatalf("new-worktree --json: %v\n%s", err, out)
	}
	wtPath, _ := filepath.EvalSymlinks(wt.Path)
	if out, err := ov("new", wtPath, "--name", "demo-wt"); err != nil {
		t.Fatalf("ovnfs new on the worktree: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(pathOf("demo-wt"), "notes.txt"), []byte("wt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "worktree", "rm", wt.Session, "--force"); err != nil {
		t.Fatalf("remove-worktree: %v\n%s", err, out)
	}
	// The message comes once the hook is done, so the checkout is gone by then.
	if err := term.WaitForText("Removed checkout demo-wt. Its changes are in", uiTimeout); err != nil {
		log, _ := tuiosCLI(t, base, "plugins", "log", "tuios.ovnfs")
		t.Errorf("no message for the removed checkout: %v\n%s\n%s", err, log, term.Snapshot())
	}
	savePNG(t, term.Screen(), hostPalette(t, ""), art, "ovnfs-worktree-removed")
	deadline = time.Now().Add(uiTimeout)
	for {
		l = ls()
		gone := true
		for _, c := range l.Checkouts {
			if c.Name == "demo-wt" {
				gone = false
			}
		}
		if gone {
			break
		}
		if time.Now().After(deadline) {
			log, _ := tuiosCLI(t, base, "plugins", "log", "tuios.ovnfs")
			t.Fatalf("the checkout of the removed worktree stays: %+v\n%s", l.Checkouts, log)
		}
		time.Sleep(200 * time.Millisecond)
	}
	snap := ""
	for _, s := range l.Snapshots {
		if s.Checkout == "demo-wt" && s.Label == "worktree removed" {
			snap = s.ID
		}
	}
	if snap == "" {
		t.Fatalf("ASSERTION: no snapshot keeps the removed checkout's change: %+v", l.Snapshots)
	}
	// The snapshot opens again with the change in it.
	if out, err := ov("new", "--from", snap, "--name", "demo-back"); err != nil {
		t.Fatalf("ASSERTION: ovnfs new --from %s: %v\n%s", snap, err, out)
	}
	if b, _ := os.ReadFile(filepath.Join(pathOf("demo-back"), "notes.txt")); string(b) != "wt\n" {
		t.Errorf("ASSERTION: the snapshot of the removed checkout holds notes.txt %q, want %q", b, "wt\n")
	}
	if out, err := ov("rm", "demo-back"); err != nil {
		t.Fatalf("ovnfs rm: %v\n%s", err, out)
	}
}
