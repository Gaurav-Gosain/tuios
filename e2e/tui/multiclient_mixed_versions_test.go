package tuie2e

import (
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuitest"
)

// Two clients of different builds on one daemon of a third, reshaping one
// tree. It covers the compatibility promise of the tree op
// (internal/session/layout_tree.go): a client too old for ops still sends its
// trees in its push, and a new client against an old daemon falls back to
// that.
//
// It runs only when the three binaries are named, because the older build is
// not something this suite builds:
//
//	TUIOS_E2E_MIX_DAEMON  the daemon, which also creates the session
//	TUIOS_E2E_MIX_A       the first client
//	TUIOS_E2E_MIX_B       the second client
//
// The second client attaches after the first has printed a styled line, so it
// reads that line from the daemon's snapshot of the pane. The snapshot crosses
// the wire as gob, and its type moved from internal/session to
// vt/snapshot. A client and a daemon on each side of the move must
// still read each other's snapshots, styles included.
//
// A mixed pair is last-writer-wins for the older client's pushes, as it was
// before the op, so a round where the screens differ for a moment is logged
// and not failed. What has to hold is the end: after a split crossing a resize
// the two clients push one layout and draw one layout.

// withBinary runs f with tuiosBin set to the binary the variable names.
func withBinary(env string, f func()) {
	prev := tuiosBin
	if b := os.Getenv(env); b != "" {
		tuiosBin = b
	}
	defer func() { tuiosBin = prev }()
	f()
}

func attachWith(t *testing.T, base, name, env string) (term *tuitest.Terminal) {
	t.Helper()
	withBinary(env, func() { term = attachIn(t, base, name, startOpts{cols: bigCols, rows: bigRows}) })
	return term
}

func TestMixedVersionsReshapeOneTree(t *testing.T) {
	if os.Getenv("TUIOS_E2E_MIX_DAEMON") == "" {
		t.Skip("TUIOS_E2E_MIX_DAEMON is not set")
	}
	const name = "mix"
	base := t.TempDir()
	writeConfig(t, base, "\n")
	withBinary("TUIOS_E2E_MIX_DAEMON", func() {
		killDaemon(t, base)
		if out, err := tuiosCLI(t, base, "new", name, "--detach"); err != nil {
			t.Fatalf("create session: %v: %s", err, out)
		}
	})
	a := attachWith(t, base, name, "TUIOS_E2E_MIX_A")
	newWindow(t, a)
	waitWindowCount(t, a, 2, "the split on the first client")
	enableTiling(t, a)
	enterTerminalMode(t, a)
	runInShell(t, a, `printf '\033[1;3mSNAP-%s-MARK\033[0m\n' $((6*7))`, "SNAP-42-MARK", shellTimeout)
	windowManagementMode(t, a)
	b := attachWith(t, base, name, "TUIOS_E2E_MIX_B")
	if err := b.WaitForText("SNAP-42-MARK", bootTimeout); err != nil {
		t.Fatalf("the second client does not show the pane's snapshot: %v\n%s", err, b.Snapshot())
	}
	scr := b.Screen()
	_, rows := scr.Size()
	for y := range rows {
		line := scr.Line(y)
		if i := strings.Index(line, "SNAP-42-MARK"); i >= 0 {
			// The frame around the pane is drawn in runes of more than one
			// byte, so the column is counted in runes.
			if c := scr.Cell(utf8.RuneCountInString(line[:i]), y); !c.Bold || !c.Italic {
				t.Errorf("the snapshot lost the line's style: %+v", c)
			}
			break
		}
	}
	withBinary("TUIOS_E2E_MIX_DAEMON", func() { waitForSettledGeometryIn(t, base, name, 2) })

	keys := [][2]string{{">", "R"}, {"R", "<"}, {">", "<"}, {"R", "R"}, {">R", "R<"}}
	differ := 0
	for round := range 15 {
		k := keys[round%len(keys)]
		together(t, a, k[0], b, k[1])
		time.Sleep(150 * time.Millisecond)
		same := false
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
			if borderSignature(a.Screen()) == borderSignature(b.Screen()) {
				same = true
				break
			}
		}
		if !same {
			differ++
			t.Logf("round %d: the screens still differ after 5s", round)
		}
	}
	t.Logf("rounds with different screens: %d of 15", differ)

	// A split on one client while the other resizes.
	together(t, a, "|", b, ">>>")
	waitWindowCount(t, a, 3, "the new pane on the first client")
	waitWindowCount(t, b, 3, "the new pane on the second client")
	var ga, gb []winRect
	withBinary("TUIOS_E2E_MIX_DAEMON", func() {
		ga = pushedGeometry(t, base, name, a, 3)
		gb = pushedGeometry(t, base, name, b, 3)
	})
	if !sameGeometry(ga, gb) {
		t.Errorf("the two clients push different layouts:\n first  %v\n second %v\n%s\n%s", ga, gb, a.Snapshot(), b.Snapshot())
	}
	if borderSignature(a.Screen()) != borderSignature(b.Screen()) {
		t.Errorf("the two clients draw different layouts\n%s\n%s", a.Snapshot(), b.Snapshot())
	}
}
