package tuie2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// churnFor is how long each subtest changes the listed folder, ten times a
// second, the rate of a build or an editor writing swap files.
const churnFor = 5 * time.Second

// TestRailFilesRefreshIsPaced changes the listed folder ten times a second
// and counts how often the client rebuilt the rail's rows.
//
// "rename" changes a name each time, so every refresh has something new to
// draw. The folder is read again at most twice a second, and the last name
// still reaches the rail when the changes stop. "swap" makes and removes a
// file each time, so every refresh reads the listing it already shows, and
// the rail is not rebuilt for it.
//
// Before the quiet refresh was paced, each refresh rebuilt the rail twice:
// once when the read was sent and once when it came back: 58 and 71 rebuilds
// against 17 and 5 now. See NEGATIVE_CONTROLS.md.
func TestRailFilesRefreshIsPaced(t *testing.T) {
	cases := []struct {
		name string
		// change is one change to the folder, the i-th.
		change func(t *testing.T, dir string, i int)
		// last is the name the rail shows once the changes stop, if any.
		last string
		// budget is the most rail rebuilds the whole run may take.
		budget uint64
	}{
		{
			name: "rename",
			change: func(t *testing.T, dir string, i int) {
				from := filepath.Join(dir, fmt.Sprintf("a-churn-%02d", i))
				to := filepath.Join(dir, fmt.Sprintf("a-churn-%02d", i+1))
				if err := os.Rename(from, to); err != nil {
					t.Fatal(err)
				}
			},
			last:   fmt.Sprintf("a-churn-%02d", int(churnFor/(100*time.Millisecond))),
			budget: 25,
		},
		{
			name: "swap",
			change: func(t *testing.T, dir string, _ int) {
				swap := filepath.Join(dir, ".brief.txt.swp")
				if err := os.WriteFile(swap, nil, 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(swap); err != nil {
					t.Fatal(err)
				}
			},
			budget: 12,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := fileViewFixture(t)
			if err := os.WriteFile(filepath.Join(dir, "a-churn-00"), nil, 0o644); err != nil {
				t.Fatal(err)
			}
			statsPath := filepath.Join(t.TempDir(), "tickstats")
			term, _ := start(t, startOpts{env: []string{"TUIOS_STATS_FILE=" + statsPath}})
			waitBoot(t, term)
			newWindow(t, term)
			waitWindowCount(t, term, 1, "opening a shell for the listing")
			enterTerminalMode(t, term)
			runInShell(t, term, "cd "+dir+" && printf '\\033]7;file://%s\\033\\\\%s\\n' \"$PWD\" lis\"\"ted", "listed", uiTimeout)
			leaveTerminalMode(t, term)
			toggleSidebarViaPalette(t, term)
			waitForAll(t, term, uiTimeout, "the first listing", "a-churn-00", "brief.txt")
			if err := term.WaitStable(uiTimeout); err != nil {
				t.Fatalf("screen never settled: %v\n%s", err, term.Snapshot())
			}

			tick := time.NewTicker(100 * time.Millisecond)
			for i := range int(churnFor / (100 * time.Millisecond)) {
				<-tick.C
				tc.change(t, dir, i)
			}
			tick.Stop()

			dirArt := artifactDir(t)
			if tc.last != "" {
				if err := term.WaitFor(func(s tuitest.Screen) bool {
					return strings.Contains(s.Text(), tc.last)
				}, uiTimeout); err != nil {
					saveArtifact(t, term, dirArt, "pace-"+tc.name+"-stale")
					t.Fatalf("the rail did not show %s once the changes stopped: %v\n%s", tc.last, err, term.Snapshot())
				}
			} else {
				time.Sleep(time.Second)
			}
			saveArtifact(t, term, dirArt, "pace-"+tc.name)

			if err := term.SendKeys(tuitest.Ctrl('b'), "q"); err != nil {
				t.Fatalf("send leader q: %v", err)
			}
			waitExit(t, term, "paced refresh quit")
			rail := readStat(t, statsPath, "rail")
			t.Logf("%s: %d rail rebuilds over the run (budget %d)", tc.name, rail, tc.budget)
			if rail > tc.budget {
				t.Fatalf("%s: the rail was rebuilt %d times, want at most %d", tc.name, rail, tc.budget)
			}
		})
	}
}

// readStat reads one named count from the line DumpTickStats writes.
func readStat(t *testing.T, path, name string) uint64 {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read tick stats %s: %v", path, err)
	}
	for field := range strings.FieldsSeq(string(data)) {
		if k, v, ok := strings.Cut(field, "="); ok && k == name {
			n, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				t.Fatalf("tick stats %s: %v", field, err)
			}
			return n
		}
	}
	t.Fatalf("tick stats %q has no %s count", data, name)
	return 0
}
