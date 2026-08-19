package session

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestProcScanFalsePositives runs the real detector over every process on the
// host. It is the only check that answers the question a fixture cannot: does
// this matcher label anything on a working machine an agent that is not one.
//
// It skips where procfs is absent, and it asserts nothing about the count, since
// a developer box legitimately runs agents. Run it with -v to read the list;
// TUIOS_PROCSCAN_STRICT=1 turns any match whose name is not a known agent into a
// failure, which is what CI on an agent-free machine should use.
func TestProcScanFalsePositives(t *testing.T) {
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("no procfs on this platform")
	}
	m := newAgentMatcher(nil)

	pids := allPIDs(t)
	var matched []string
	for _, pid := range pids {
		info := foregroundInfo{
			comm: readComm(pid),
			argv: readCmdline(pid),
			exe:  readExe(pid),
		}
		if info.comm == "" && len(info.argv) == 0 {
			continue
		}
		id, ok := m.identify(info)
		if !ok {
			continue
		}
		label := id
		if label == "" {
			label = "(name list)"
		}
		matched = append(matched, strconv.Itoa(pid)+" "+label+
			" comm="+info.comm+" exe="+info.exe+" argv="+strings.Join(info.argv, " "))
	}
	sort.Strings(matched)

	t.Logf("scanned %d processes, %d matched as agents", len(pids), len(matched))
	for _, line := range matched {
		t.Logf("  %s", line)
	}
	if os.Getenv("TUIOS_PROCSCAN_STRICT") == "1" && len(matched) > 0 {
		t.Errorf("strict mode: %d processes matched as agents", len(matched))
	}
}

func allPIDs(t *testing.T) []int {
	t.Helper()
	entries, err := filepath.Glob("/proc/[0-9]*")
	if err != nil {
		t.Fatalf("glob /proc: %v", err)
	}
	out := make([]int, 0, len(entries))
	for _, e := range entries {
		pid, err := strconv.Atoi(filepath.Base(e))
		if err == nil {
			out = append(out, pid)
		}
	}
	return out
}
