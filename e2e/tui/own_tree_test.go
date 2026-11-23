package tuie2e

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/testutil"
)

// TestAFanPaneClosesAndWaitsForItsOwnTree gives a shell pane, lead, the grants
// read, write and fan, and lets it start a tree: gen1 in its own session,
// which starts gen0 from its own pane, and far, the one attempt of a fan, in
// a session of its own. The stranger panes, opened from outside every pane,
// are outside the tree, one in lead's session and one in the fan's.
//
// The pane waits with --select on state:idle, which matches the strangers
// too: the answer must list gen1, gen0 and far, and no stranger. It closes
// gen0, its grandchild, by name. A stranger renamed to gen0 is then refused,
// as are the strangers by id in both sessions, so each needs admin as before.
// It closes far, in the other session, by its id with no -s. A stranger
// there named by its id with no -s is refused too. With the fan grant taken away, the
// close of gen1 and the wait are refused. With fan back, gen1 closes.
//
// Negative controls: with holdToOwnTree's ok always false, the pane's
// wait-for --select is refused. With inOwnTree always true, the wait lists
// the strangers and the strangers close. With the under filter cut from
// waitAgentStateSelect, the wait lists the strangers.
func TestAFanPaneClosesAndWaitsForItsOwnTree(t *testing.T) {
	base := t.TempDir()
	startHandoffDaemon(t, base)
	const sess = "e2e-tree"
	agent := writeChainAgent(t, base)
	repo := testutil.GitRepo(t)
	outDir := t.TempDir()
	lead := newCommandWindow(t, base, sess, "lead", false, "sh")
	stranger := newCommandWindow(t, base, sess, "stranger", false, "sh", agent, "0")
	if out, err := handoffCLI(t, base, 30*time.Second, "set-pane-grants", "-s", sess, "-w", lead, "--grants", "read,write,fan"); err != nil {
		t.Fatalf("set-pane-grants: %v\n%s", err, out)
	}
	evidence := map[string]any{"lead": lead, "stranger": stranger}

	// run types line into lead, with its output and exit status in a file,
	// and returns the file's text once the line has ended.
	step := 0
	run := func(line string) string {
		t.Helper()
		step++
		path := filepath.Join(outDir, strconv.Itoa(step)+".txt")
		typed := line + " >'" + path + "' 2>&1; echo EXIT=$? >>'" + path + "'\n"
		if out, err := handoffCLI(t, base, 30*time.Second, "send-text", "-s", sess, "-w", lead, typed); err != nil {
			t.Fatalf("send-text into lead: %v\n%s", err, out)
		}
		text := waitFileHas(t, path, "lead's step "+strconv.Itoa(step), 60*time.Second, func(s string) bool {
			return strings.Contains(s, "EXIT=")
		})
		evidence["step"+strconv.Itoa(step)] = line + "\n" + text
		return text
	}
	T := "'" + tuiosBin + "'"
	served := func(text string) bool { return strings.Contains(text, "EXIT=0") }

	if out := run(T + " start-agent -s " + sess + " '" + agent + " 1' --name gen1 --no-wait"); !served(out) {
		t.Fatalf("lead could not start gen1:\n%s", out)
	}
	if out := run("cd '" + repo + "' && " + T + " fan 1 --agent '" + agent + " 0' --name far hello"); !served(out) {
		t.Fatalf("lead could not start the fan:\n%s", out)
	}
	other := ""
	waitUntil(t, "the fan's session to open", 30*time.Second, func() (bool, string) {
		names := sessionNames(t, base)
		for _, name := range names {
			if name != sess {
				other = name
			}
		}
		return other != "", strings.Join(names, ", ")
	})
	evidence["fan_session"] = other
	farStranger := newCommandWindow(t, base, other, "farstranger", false, "sh", agent, "0")
	evidence["far_stranger"] = farStranger
	ids := map[string]string{}
	farName := ""
	waitUntil(t, "the tree and the strangers to be idle", 60*time.Second, func() (bool, string) {
		rows := linkRows(t, base, map[string]any{"session": sess})
		farRows := linkRows(t, base, map[string]any{"session": other})
		for name, r := range farRows {
			if name != "farstranger" {
				farName = name
				name = "far"
			}
			rows[name] = r
		}
		for _, name := range []string{"gen1", "gen0", "far", "stranger", "farstranger"} {
			r, ok := rows[name]
			if !ok || r.State != "idle" {
				return false, "rows: " + strings.Join(slices.Sorted(maps.Keys(rows)), ", ")
			}
			ids[name] = r.Window
		}
		return true, ""
	})

	// The selector matches the strangers too. The wait keeps to the tree.
	out := run(T + " wait-for agent-state --select state:idle --until idle --every --timeout 20000 --json")
	var answer struct {
		Panes []struct {
			Window string `json:"window"`
		} `json:"panes"`
	}
	start := strings.Index(out, "{")
	if !served(out) || start < 0 || json.Unmarshal([]byte(out[start:strings.LastIndex(out, "}")+1]), &answer) != nil {
		t.Fatalf("ASSERTION: lead's wait-for --select was not served:\n%s", out)
	}
	var got []string
	for _, p := range answer.Panes {
		got = append(got, p.Window)
	}
	want := []string{ids["gen1"], ids["gen0"], ids["far"]}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("ASSERTION: the wait answered panes %v, want the tree %v and no stranger", got, want)
	}

	// The grandchild closes by name.
	if out := run(T + " close-window -s " + sess + " gen0"); !served(out) {
		t.Errorf("ASSERTION: lead could not close its grandchild gen0:\n%s", out)
	}
	// A stranger renamed to gen0 is still a stranger.
	if out, err := handoffCLI(t, base, 30*time.Second, "set-window", "-s", sess, "-w", stranger, "--name", "gen0"); err != nil {
		t.Fatalf("rename the stranger: %v\n%s", err, out)
	}
	for _, target := range []string{"-s " + sess + " gen0", "-s " + sess + " " + stranger, "-s " + other + " " + farStranger, farStranger} {
		if out := run(T + " close-window " + target); served(out) || !strings.Contains(out, "admin grant") {
			t.Errorf("ASSERTION: close-window %s outside the tree was not refused for the admin grant:\n%s", target, out)
		}
	}
	// A child in another session closes by its id, with no session named.
	if out := run(T + " close-window " + ids["far"]); !served(out) {
		t.Errorf("ASSERTION: lead could not close far in the other session:\n%s", out)
	}

	// Without fan, the tree is not the pane's to close or wait for.
	if out, err := handoffCLI(t, base, 30*time.Second, "set-pane-grants", "-s", sess, "-w", lead, "--grants", "read,write"); err != nil {
		t.Fatalf("set-pane-grants: %v\n%s", err, out)
	}
	if out := run(T + " close-window -s " + sess + " gen1"); served(out) || !strings.Contains(out, "admin grant") {
		t.Errorf("ASSERTION: close-window of gen1 without fan was not refused:\n%s", out)
	}
	if out := run(T + " wait-for agent-state --select state:idle --until idle --timeout 2000"); served(out) || !strings.Contains(out, "select reaches every session") {
		t.Errorf("ASSERTION: wait-for --select without fan was not refused:\n%s", out)
	}
	if out, err := handoffCLI(t, base, 30*time.Second, "set-pane-grants", "-s", sess, "-w", lead, "--grants", "read,write,fan"); err != nil {
		t.Fatalf("set-pane-grants: %v\n%s", err, out)
	}
	if out := run(T + " close-window -s " + sess + " gen1"); !served(out) {
		t.Errorf("ASSERTION: lead could not close its child gen1:\n%s", out)
	}

	left := linkRows(t, base, map[string]any{"session": sess, "all": true})
	maps.Copy(left, linkRows(t, base, map[string]any{"session": other, "all": true}))
	evidence["left"] = left
	for _, name := range []string{"gen1", farName} {
		if _, ok := left[name]; ok {
			t.Errorf("ASSERTION: %s is still open after lead closed it", name)
		}
	}
	for _, id := range []string{stranger, farStranger} {
		found := false
		for _, r := range left {
			found = found || r.Window == id
		}
		if !found {
			t.Errorf("ASSERTION: stranger %s closed, and lead does not hold admin", id)
		}
	}
	if dir := os.Getenv("TUIOS_E2E_FRAMES"); dir != "" {
		t.Logf("frames under %s", dir)
	}
	writeHandoffArtifact(t, evidence)
}
