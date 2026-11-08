package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
	"tuios.dev/shot"
)

// codexBoxScript is a stand-in for the Codex composer as codex-cli 0.166.0
// and 0.167.0 draw it with no status line setting (issue #1588): the bold
// glyph and the faint placeholder, an empty row, then two footer rows, the
// status line and the key hints. The key-hint row with "for agents" shows
// only while the box is empty. Typed text replaces the placeholder and hides
// that row. Enter moves the line above the box as "sent: ...", and Ctrl+U
// clears the box.
const codexBoxScript = `stty -icanon -echo min 1 2>/dev/null
draft=""
sent=""
draw() {
	printf '\033[H\033[2J'
	printf 'fakecodex ready\n%s\n\n' "$sent"
	if [ -z "$draft" ]; then
		printf '\033[1m\342\200\272\033[0m \033[2mAsk Codex to do anything\033[0m\n\n'
		printf '  GPT-6.1-Sol medium \302\267 ~/dev/demo \302\267 demo \302\267 Main [default]\n'
		printf '  \342\206\220 for agents \302\267 ? for shortcuts\n'
	else
		printf '\033[1m\342\200\272\033[0m %s\n\n' "$draft"
		printf '  GPT-6.1-Sol medium \302\267 ~/dev/demo \302\267 demo \302\267 Main [default]\n'
	fi
}
draw
while IFS= read -r -n1 -d '' c; do
	case "$c" in
	$'\r' | $'\n') sent="sent: $draft"; draft="" ;;
	$'\x15') draft="" ;;
	$'\x7f') draft="${draft%?}" ;;
	*) draft="$draft$c" ;;
	esac
	draw
done
`

// TestQueuedMessageReachesAnEmptyCodexBox is issue #1588. Codex draws two
// footer rows under an empty composer, and the prompt_box rule allowed one,
// so the daemon read the empty box as the person's draft. A mail wake or a
// tuios queue entry then stayed held for hours on an idle pane. An empty box
// must let a queued entry through at rest, and a box with typed text must
// still hold one.
func TestQueuedMessageReachesAnEmptyCodexBox(t *testing.T) {
	term, base := attachClientBase(t)
	const sess = "e2e-ctrlp"
	cli := func(what string, args ...string) string {
		t.Helper()
		out, err := tuiosCLI(t, base, args...)
		if err != nil {
			t.Fatalf("%s: %v\n%s", what, err, out)
		}
		return out
	}
	held := func() bool {
		return strings.Contains(strings.ReplaceAll(cli("get-agent-state", "get-agent-state", "-s", sess, "--json"), " ", ""), `"draft_held":true`)
	}
	script := filepath.Join(t.TempDir(), "codex.sh")
	if err := os.WriteFile(script, []byte(codexBoxScript), 0o644); err != nil {
		t.Fatal(err)
	}
	cli("start the stand-in agent", "send-text", "-s", sess, "bash "+script+"\n")
	if err := term.WaitForText("fakecodex ready", uiTimeout); err != nil {
		t.Fatalf("the stand-in agent never drew its box: %v\n%s", err, term.Snapshot())
	}
	if err := term.WaitForText("for agents", uiTimeout); err != nil {
		t.Fatalf("the stand-in agent never drew its footer: %v\n%s", err, term.Snapshot())
	}
	cli("set working", "set-agent-state", "-s", sess, "working", "--harness", "codex")
	cli("set idle", "set-agent-state", "-s", sess, "idle", "--harness", "codex")

	// The box is empty: the queued entry is typed.
	const first = "queued-into-empty-codex-91d2"
	cli("queue at rest", "queue", "-s", sess, "echo", first)
	if err := term.WaitForText("sent: echo "+first, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: the queued entry was never typed into the empty Codex box (draft_held %v)\n%s", held(), term.Snapshot())
	}
	if held() {
		t.Fatalf("ASSERTION: the daemon still reads the empty Codex box as a draft\n%s", term.Snapshot())
	}
	dir := artifactDir(t)
	saveArtifact(t, term, dir, "codex-empty-box-typed")
	savePNG(t, term.Screen(), shot.XTermPalette(), dir, "codex-empty-box-typed")

	// The person types into the box: the next entry waits for them.
	cli("set working", "set-agent-state", "-s", sess, "working", "--harness", "codex")
	cli("set idle", "set-agent-state", "-s", sess, "idle", "--harness", "codex")
	enterTerminalMode(t, term)
	const draft, second = "half a thought", "queued-behind-codex-draft-4b7e"
	if err := term.Type(draft); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("› "+draft, uiTimeout); err != nil {
		t.Fatalf("the draft never reached the box: %v\n%s", err, term.Snapshot())
	}
	cli("queue behind the draft", "queue", "-s", sess, "echo", second)
	deadline := time.Now().Add(uiTimeout)
	for !held() {
		if strings.Contains(term.Screen().Text(), second) {
			t.Fatalf("ASSERTION: the queued entry was typed into the person's Codex draft\n%s", term.Snapshot())
		}
		if time.Now().After(deadline) {
			t.Fatalf("ASSERTION: the daemon never held the queue for the Codex draft\n%s", term.Snapshot())
		}
		time.Sleep(200 * time.Millisecond)
	}
	if strings.Contains(term.Screen().Text(), second) {
		t.Fatalf("ASSERTION: the queued entry was typed into the person's Codex draft\n%s", term.Snapshot())
	}
	saveArtifact(t, term, dir, "codex-draft-holds")
	if err := term.SendKeys(tuitest.Ctrl('u')); err != nil {
		t.Fatal(err)
	}
	if err := term.WaitForText("sent: echo "+second, uiTimeout); err != nil {
		t.Fatalf("the queued entry was never typed after the draft was cleared: %v\n%s", err, term.Snapshot())
	}
}
