package session

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

// TestProcScanFalsePositives runs the detector over every process on the host and
// compares it against the predicate that shipped. It is the check a fixture
// cannot make: does this matcher label anything on a working machine an agent
// that is not one.
//
// It skips where procfs is absent and asserts no count, since a developer box
// legitimately runs agents and the two lists are not nested. The gated matcher
// drops every process that merely had an agent's name in an argument, and picks
// up ones the old rules could not see at all, because every exe_glob they carried
// was dead and a launcher that rewrites comm and argv[0] left nothing to match.
// Run with -v to read both lists; that comparison is the evidence.
//
// What it does assert is the rule that makes a false positive possible: no match
// may rest on argv unless the process is an interpreter standing in for the
// program named there. That guard is what a future predicate reintroducing a
// scan of the whole command line would trip over.
//
// TUIOS_PROCSCAN_STRICT=1 additionally fails on any match at all, which is what
// CI on a machine running no agents should use.
func TestProcScanFalsePositives(t *testing.T) {
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("no procfs on this platform")
	}
	m := newAgentMatcher(nil)
	reg, _ := harness.Load(harness.UserDir())

	pids := allPIDs(t)
	var now, before []string
	for _, pid := range pids {
		info := readProcessInfo(pid)
		if info.comm == "" && len(info.argv) == 0 {
			continue
		}
		if id, rule, ok := m.identifyDetail(info); ok {
			now = append(now, describeMatch(pid, id, info))
			if strings.Contains(rule, "argv_path=") && info.proc().RunToken() == "" {
				t.Errorf("pid %d matched %q on %s, but it is not an interpreter: "+
					"an argument is not an identity", pid, id, rule)
			}
		}
		if id, ok := legacyIdentify(reg, info); ok {
			before = append(before, describeMatch(pid, id, info))
		}
	}
	sort.Strings(now)
	sort.Strings(before)

	t.Logf("scanned %d processes", len(pids))
	t.Logf("the shipped substring scan matched %d:", len(before))
	for _, line := range before {
		t.Logf("  %s", line)
	}
	t.Logf("the gated matcher matches %d:", len(now))
	for _, line := range now {
		t.Logf("  %s", line)
	}

	if os.Getenv("TUIOS_PROCSCAN_STRICT") == "1" && len(now) > 0 {
		t.Errorf("strict mode: %d processes matched as agents", len(now))
	}
}

func describeMatch(pid int, id string, info foregroundInfo) string {
	if id == "" {
		id = "(name list)"
	}
	return strconv.Itoa(pid) + " " + id +
		" comm=" + info.comm + " exe=" + info.exe + " argv=" + strings.Join(info.argv, " ")
}

// legacyIdentify is the predicate that shipped, kept verbatim so the scan reports
// what changed rather than only what is true now.
//
// It scanned every argv token for a substring of any manifest's argv_path, which
// is what made "tail -f ~/dev/opencode/main.go" opencode, and it matched exe_glob
// with path.Match, whose "*" cannot cross a "/", which is why every glob in every
// shipped manifest matched nothing at all.
func legacyIdentify(reg *harness.Registry, info foregroundInfo) (string, bool) {
	if reg == nil {
		return "", false
	}
	commBase := harness.BaseName(info.comm)
	argv0 := ""
	if len(info.argv) > 0 {
		argv0 = harness.BaseName(info.argv[0])
	}
	for _, id := range reg.IDs() {
		d := reg.Lookup(id).Detect
		if legacyContains(d.Comm, commBase) || legacyContains(d.Argv0, argv0) {
			return id, true
		}
		for _, want := range d.ArgvPath {
			for _, arg := range info.argv {
				if strings.Contains(strings.ToLower(arg), want) {
					return id, true
				}
			}
		}
		if info.exe != "" {
			lower := strings.ToLower(info.exe)
			for _, pattern := range d.ExeGlob {
				if ok, err := path.Match(pattern, lower); err == nil && ok {
					return id, true
				}
				if ok, err := filepath.Match(pattern, lower); err == nil && ok {
					return id, true
				}
			}
		}
	}
	return "", false
}

func legacyContains(list []string, v string) bool {
	if v == "" {
		return false
	}
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
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
