package tuie2e

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// One e2e run keeps everything it makes in the temp dir under one folder of its
// own, /tmp/tuios-e2e-run-PID, and TMPDIR names it for the run, so t.TempDir,
// os.MkdirTemp("", ...) and every program the tests start (go build, tuios)
// write there. The folder is removed when the run ends.
//
// A test that makes a folder with os.MkdirTemp and does not remove it used to
// leave it in /tmp for good: the once-per-run builds of tuios (sync-next,
// restart-mid, other-protocol) and the image-number stand-in were a hundred
// megabytes each, and a machine that ran the suite a few times held 2.5 GB of
// them. Now a run that ends with anything of that kind in its folder fails and
// names it with its size, so the test that left it is the one that gets fixed,
// and a run that is killed leaves a folder the next run removes.

// runTempPrefix names a run's folder; the pid follows it.
const runTempPrefix = "tuios-e2e-run-"

// runTempMark is the file that marks a folder as an e2e run's own.
const runTempMark = ".tuios-e2e-run"

var (
	runTempRoot string

	runScratchMu    sync.Mutex
	runScratchPaths = map[string]bool{}
)

// startRunTemp makes the run's folder and points TMPDIR at it. It first removes
// the folders of runs that ended without removing theirs (killed, timed out).
func startRunTemp() error {
	sweepStaleRunTemps()
	root := filepath.Join("/tmp", runTempPrefix+strconv.Itoa(os.Getpid()))
	// A folder left by an earlier run that had this pid is not this run's.
	if err := os.RemoveAll(root); err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	// The mark says the folder is a run's own: the sweep removes only a folder
	// that has it, and the guard reads only the folder it made, so one run never
	// blames or removes the folders of another run on the same machine.
	if err := os.WriteFile(filepath.Join(root, runTempMark), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		return err
	}
	runTempRoot = root
	if err := os.Setenv("TMPDIR", root); err != nil {
		return err
	}
	// A guard that reads a folder nothing writes to passes for nothing. Check
	// that the temp dir the tests will use is this one.
	probe, err := os.MkdirTemp("", "probe")
	if err != nil {
		return err
	}
	defer os.RemoveAll(probe)
	if filepath.Dir(probe) != root {
		return fmt.Errorf("os.MkdirTemp made %s, not under %s: TMPDIR is not honoured", probe, root)
	}
	return nil
}

// runScratchDir makes a folder in the run's folder that stays until the run
// ends: the once-per-run builds that every test of a kind shares. It is not a
// leftover for finishRunTemp, which removes it with the rest.
func runScratchDir(prefix string) (string, error) {
	dir, err := os.MkdirTemp("", prefix)
	if err != nil {
		return "", err
	}
	runScratchMu.Lock()
	runScratchPaths[dir] = true
	runScratchMu.Unlock()
	return dir, nil
}

// finishRunTemp returns the run's exit code, 1 when a test left something in the
// run's folder, and removes the folder.
func finishRunTemp(code int) int {
	if runTempRoot == "" {
		return code
	}
	left := leftInRunTemp(runTempRoot)
	rmErr := os.RemoveAll(runTempRoot)
	// A folder that is still there after RemoveAll is held: a daemon or a
	// program of a test still writes into it, or it cannot be read.
	if _, err := os.Lstat(runTempRoot); err == nil || rmErr != nil {
		left = append(left, fmt.Sprintf("  %s itself could not be removed (%v); something still holds or writes into it", runTempRoot, rmErr))
		left = append(left, leftInRunTemp(runTempRoot)...)
	}
	if len(left) == 0 {
		return code
	}
	fmt.Fprintf(os.Stderr, "\ne2e: the tests left %d entries in %s. A test or a shell command it runs that makes one (os.MkdirTemp, mktemp) removes it, or uses t.TempDir:\n%s\n", len(left), runTempRoot, strings.Join(left, "\n"))
	if code == 0 {
		return 1
	}
	return code
}

// leftInRunTemp lists what is in root and not a once-per-run folder, as "name
// (size)" lines, largest first.
func leftInRunTemp(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	runScratchMu.Lock()
	defer runScratchMu.Unlock()
	type item struct {
		line string
		size int64
	}
	var items []item
	for _, e := range entries {
		path := filepath.Join(root, e.Name())
		if runScratchPaths[path] || e.Name() == runTempMark {
			continue
		}
		size := folderBytes(path)
		items = append(items, item{fmt.Sprintf("  %s (%s)%s", e.Name(), byteWords(size), leakedPreview(path, e)), size})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].size > items[j].size })
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.line
	}
	return out
}

// leakedPreview shows what a small leaked file holds, its first lines, or the
// first names in a leaked folder, so the leak names the program that made it.
func leakedPreview(path string, e fs.DirEntry) string {
	const maxBytes, maxLines, maxWidth = 4096, 3, 120
	if e.IsDir() {
		names, _ := os.ReadDir(path)
		var list []string
		for i, n := range names {
			if i == 5 {
				list = append(list, "...")
				break
			}
			list = append(list, n.Name())
		}
		if len(list) == 0 {
			return ": empty folder"
		}
		return ": holds " + strings.Join(list, ", ")
	}
	info, err := e.Info()
	if err != nil || info.Size() == 0 || info.Size() > maxBytes {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var out strings.Builder
	for i, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if i == maxLines {
			break
		}
		if len(line) > maxWidth {
			line = line[:maxWidth] + "..."
		}
		fmt.Fprintf(&out, "\n      | %s", strings.ToValidUTF8(line, "?"))
	}
	return out.String()
}

func folderBytes(path string) int64 {
	var n int64
	_ = filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}

func byteWords(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// sweepStaleRunTemps removes the folders of runs that are over: the pid in the
// name is not running, and the folder is this user's own.
func sweepStaleRunTemps() {
	entries, err := os.ReadDir("/tmp")
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), runTempPrefix) {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimPrefix(e.Name(), runTempPrefix))
		if err != nil || pid == os.Getpid() {
			continue
		}
		path := filepath.Join("/tmp", e.Name())
		if _, err := os.Stat(filepath.Join(path, runTempMark)); err != nil {
			continue
		}
		if processAlive(pid) || !ownedByMe(path) {
			continue
		}
		_ = os.RemoveAll(path)
	}
}
