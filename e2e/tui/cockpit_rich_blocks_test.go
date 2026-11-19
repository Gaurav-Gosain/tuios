package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// richBlocksAnswer is an agent's reply with a TML block, a TML block that is
// not valid, and a Mermaid block last, so C copies the Mermaid one.
const richBlocksAnswer = "The state of the build:\n\n" +
	"```tml\n<row gap=\"2\"><badge role=\"success\">Ready</badge><progress value=\"0.6\" label=\"60 percent\"/></row>\n```\n\n" +
	"A broken one:\n\n" +
	"```tml\n<nosuch/>\n```\n\n" +
	"The flow:\n\n" +
	"```mermaid\nflowchart LR\n  A[Plan] --> B[Ship]\n```\n"

// richBlocksSource is the Mermaid block's source.
const richBlocksSource = "flowchart LR\n  A[Plan] --> B[Ship]"

// TestCockpitThreadDrawsRichBlocks shows an agent's reply that holds a TML
// block, a broken TML block and a Mermaid block. The TML block is drawn as a
// static picture: its badge and its progress label, and no tag. The broken
// one shows its source under a line that says why it was not drawn. The
// Mermaid block is drawn as a diagram: the two node names, and no source. A
// click on the diagram's header shows the source, and a second click the
// diagram again. P does the same. C copies the lowest block, the diagram, as its source. The
// artifacts are the frames rich-blocks and rich-blocks-source.
func TestCockpitThreadDrawsRichBlocks(t *testing.T) {
	base, term := openTextThread(t, richBlocksAnswer, "The flow:")
	saveCockpitFrames(t, term, "rich-blocks")
	railLists(t, term, "the blocks are drawn", []string{"Ready", "60 percent", "Plan", "Ship", "Not drawn"},
		[]string{"<badge", "<row", "flowchart LR"})

	// The header of the diagram, the lowest block, toggles it with its source.
	head := lastRowWith(term.Screen(), "show the source")
	if head < 0 {
		t.Fatalf("ASSERTION: no block header offers the source\n%s", term.Snapshot())
	}
	col := strings.Index(term.Screen().Line(head), "show the source")
	mouseClick(t, term, len([]rune(term.Screen().Line(head)[:col]))+1, head, tuitest.MouseLeft, 0)
	railLists(t, term, "a click on the header shows the source", []string{"flowchart LR", "show the picture"}, nil)
	saveCockpitFrames(t, term, "rich-blocks-source")
	// The header is in the same place, and a second press there within the
	// double click time would be a double click, which selects a word.
	pause(t, "a click, not a double click", time.Second)
	head = rowWith(term.Screen(), "show the picture")
	col = strings.Index(term.Screen().Line(head), "show the picture")
	mouseClick(t, term, len([]rune(term.Screen().Line(head)[:col]))+1, head, tuitest.MouseLeft, 0)
	railLists(t, term, "a second click shows the diagram", []string{"Plan", "show the source"}, []string{"flowchart LR"})

	// P does the same for the lowest block on screen, the diagram.
	sendKeys(t, term, "P")
	railLists(t, term, "P shows the source", []string{"flowchart LR", "show the picture"}, nil)
	sendKeys(t, term, "P")
	railLists(t, term, "P again shows the diagram", []string{"Plan", "show the source"}, []string{"flowchart LR"})

	// C copies the lowest block as its source.
	before := mustCLIOr(t, base, "show-buffer")
	if err := term.SendKeys("C"); err != nil {
		t.Fatal(err)
	}
	got := before
	waitUntil(t, "ASSERTION: C copies the diagram's source", uiTimeout, func() (bool, string) {
		got = mustCLIOr(t, base, "show-buffer")
		return got == richBlocksSource, got
	})
	alive(t, term, "after the rich blocks")
}
