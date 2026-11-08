package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// kittyKeyDump turns on the kitty keyboard protocol in its pane, as fish 4
// and Codex do, and prints each byte it reads in hex, one per row: "B1b".
const kittyKeyDump = `stty raw -echo
printf '\033[>1u'
echo KEYDUMP-READY
while IFS= read -r -n1 -d '' c; do
	printf 'B%s\r\n' "$(printf '%s' "$c" | od -An -tx1 | tr -d ' \n')"
done
`

// TestF3ReachesAKittyKeyboardPane is issue #1615. A pane that turned on the
// kitty keyboard protocol got F3 as CSI R. The protocol moved F3 to CSI 13 ~
// because CSI R is the cursor position report, so fish 4 read no key and
// Codex's F3 shortcut did nothing. F3 from the host, in its legacy form and in
// the kitty form, must reach the pane as CSI 13 ~.
func TestF3ReachesAKittyKeyboardPane(t *testing.T) {
	term, base := attachClientBase(t)
	script := filepath.Join(t.TempDir(), "keys.sh")
	if err := os.WriteFile(script, []byte(kittyKeyDump), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := tuiosCLI(t, base, "send-text", "-s", "e2e-ctrlp", "bash "+script+"\n"); err != nil {
		t.Fatalf("start the key dump: %v\n%s", err, out)
	}
	if err := term.WaitForText("KEYDUMP-READY", uiTimeout); err != nil {
		t.Fatalf("the key dump never started: %v\n%s", err, term.Snapshot())
	}
	enterTerminalMode(t, term)

	// rows is what the pane printed after the ready line, one byte per row.
	rows := func(s tuitest.Screen) string {
		_, after, _ := strings.Cut(s.Text(), "KEYDUMP-READY")
		var out []string
		for l := range strings.SplitSeq(after, "\n") {
			for f := range strings.FieldsSeq(strings.ReplaceAll(l, "│", " ")) {
				if strings.HasPrefix(f, "B") {
					out = append(out, f)
				}
			}
		}
		return strings.Join(out, " ")
	}
	const f3 = "B1b B5b B31 B33 B7e"
	for i, host := range []string{"\x1bOR", "\x1b[13~"} {
		if err := term.Type(host); err != nil {
			t.Fatal(err)
		}
		want := strings.TrimSpace(strings.Repeat(f3+" ", i+1))
		if err := term.WaitFor(func(s tuitest.Screen) bool { return strings.HasPrefix(rows(s), want) }, 5*time.Second); err != nil {
			t.Fatalf("ASSERTION: F3 sent as %q reached the pane as %q, want CSI 13 ~ (%s)\n%s", host, rows(term.Screen()), f3, term.Snapshot())
		}
	}
	saveArtifact(t, term, artifactDir(t), "f3-in-kitty-pane")
}
