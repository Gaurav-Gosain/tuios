package tuie2e

import (
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuitest"
)

// richBlocksAnswer is an agent's reply with a TML block, a TML block that is
// not valid, and a Mermaid block, the last block, so C copies the Mermaid one.
// A line of text follows the diagram.
const richBlocksAnswer = "The state of the build:\n\n" +
	"```tml\n<row gap=\"2\"><badge role=\"success\">Ready</badge><progress value=\"0.6\" label=\"60 percent\"/></row>\n```\n\n" +
	"A broken one:\n\n" +
	"```tml\n<nosuch/>\n```\n\n" +
	"The flow:\n\n" +
	"```mermaid\nflowchart LR\n  A[Plan] --> B[Ship]\n```\n\n" +
	"Then it ships.\n"

// richBlocksSource is the Mermaid block's source.
const richBlocksSource = "flowchart LR\n  A[Plan] --> B[Ship]"

// TestCockpitThreadDrawsRichBlocks shows an agent's reply that holds a TML
// block, a broken TML block and a Mermaid block, on a host with kitty
// graphics. The TML block is drawn as a static picture: its badge and its
// progress label, and no tag. The broken one shows its source under a line
// that says why it was not drawn. The Mermaid block is a kitty picture under
// its header, the default of [agents] mermaid: the host gets a placement on
// the rows under the header, and the node names are not text.
//
// A click on "text" in the header draws the diagram as text: the two node
// names, and the picture leaves the host. T draws it as a picture again, and
// T once more as text. A click on the rest of the header shows the source,
// and a second click the diagram again. P does the same. C copies the lowest
// block, the diagram, as its source. The artifacts are the frames
// rich-blocks-picture, rich-blocks-text, rich-blocks-picture-again and
// rich-blocks-source.
func TestCockpitThreadDrawsRichBlocks(t *testing.T) {
	host := &lockedBuffer{}
	base, term := openTextThreadOn(t, richBlocksAnswer, "", host, "The flow:")
	railLists(t, term, "the blocks are drawn", []string{"Ready", "60 percent", "Not drawn", "picture / text"},
		[]string{"<badge", "<row", "flowchart LR"})

	// The diagram is a picture on the rows under its header.
	head := lastRowWith(term.Screen(), "picture / text")
	pic := waitPlaced(t, term, host, func(p hostPlace) bool { return p.row == head+1 && p.rows > 0 })
	saveCockpitFrames(t, term, "rich-blocks-picture", hostPictures(t, []byte(host.String()))...)
	// The picture takes the rows it draws on, and the next line follows it
	// after one blank row at most.
	if next := lastRowWith(term.Screen(), "Then it ships."); next < 0 || next > pic.row+pic.rows+1 {
		t.Fatalf("ASSERTION: the picture takes rows %d to %d, and the next line is on row %d, more than one row under it\n%s",
			pic.row, pic.row+pic.rows-1, next, term.Snapshot())
	}
	if strings.Contains(term.Screen().Text(), "Plan") || strings.Contains(term.Screen().Text(), "Ship") {
		t.Fatalf("ASSERTION: the diagram is a picture, and its node names are drawn as text too\n%s", term.Snapshot())
	}

	// A click on "text" in the header draws it as text.
	clickWord(t, term, head, "text   show the source")
	railLists(t, term, "a click on text draws the diagram as text", []string{"Plan", "Ship", "picture / text"}, []string{"flowchart LR"})
	waitUnplaced(t, term, host, pic.img, "the diagram is text")
	saveCockpitFrames(t, term, "rich-blocks-text", hostPictures(t, []byte(host.String()))...)

	// T draws it as a picture again, and T once more as text.
	sendKeys(t, term, "T")
	railLists(t, term, "T draws the diagram as a picture", []string{"picture / text"}, []string{"Plan", "Ship"})
	head = lastRowWith(term.Screen(), "picture / text")
	waitPlaced(t, term, host, func(p hostPlace) bool { return p.row == head+1 && p.rows > 0 })
	saveCockpitFrames(t, term, "rich-blocks-picture-again", hostPictures(t, []byte(host.String()))...)
	sendKeys(t, term, "T")
	railLists(t, term, "T again draws the diagram as text", []string{"Plan", "Ship"}, nil)
	waitUnplaced(t, term, host, pic.img, "T drew the diagram as text")

	// The rest of the header shows the source.
	head = lastRowWith(term.Screen(), "show the source")
	if head < 0 {
		t.Fatalf("ASSERTION: no block header offers the source\n%s", term.Snapshot())
	}
	// A press on the header right after the one on its switch would be a
	// double click, which selects a word.
	pause(t, "a click, not a double click", time.Second)
	clickWord(t, term, head, "show the source")
	railLists(t, term, "a click on the header shows the source", []string{"flowchart LR", "show the diagram"}, nil)
	saveCockpitFrames(t, term, "rich-blocks-source")
	pause(t, "a click, not a double click", time.Second)
	clickWord(t, term, rowWith(term.Screen(), "show the diagram"), "show the diagram")
	railLists(t, term, "a second click shows the diagram", []string{"Plan", "show the source"}, []string{"flowchart LR"})

	// P does the same for the lowest block on screen, the diagram.
	sendKeys(t, term, "P")
	railLists(t, term, "P shows the source", []string{"flowchart LR", "show the diagram"}, nil)
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

// TestCockpitThreadMermaidTextSetting is [agents] mermaid = "text" on a host
// with kitty graphics: the diagram is drawn as text, and no picture reaches
// the host. A click on "picture" in its header draws it as a picture. The
// artifact is the frame mermaid-setting-text.
func TestCockpitThreadMermaidTextSetting(t *testing.T) {
	host := &lockedBuffer{}
	_, term := openTextThreadOn(t, richBlocksAnswer, "mermaid = \"text\"\n", host, "The flow:")
	railLists(t, term, "the diagram is text", []string{"Plan", "Ship", "picture / text"}, []string{"flowchart LR"})
	saveCockpitFrames(t, term, "mermaid-setting-text")
	if n := len(livePlaces([]byte(host.String()))); n != 0 {
		t.Fatalf("ASSERTION: mermaid = \"text\" and %d pictures are placed: %+v", n, livePlaces([]byte(host.String())))
	}
	head := lastRowWith(term.Screen(), "picture / text")
	clickWord(t, term, head, "picture / text")
	railLists(t, term, "a click on picture draws the diagram as a picture", []string{"picture / text"}, []string{"Plan", "Ship"})
	head = lastRowWith(term.Screen(), "picture / text")
	waitPlaced(t, term, host, func(p hostPlace) bool { return p.row == head+1 && p.rows > 0 })
	alive(t, term, "after the setting")
}

// clickWord clicks the first cell of word on screen row row.
func clickWord(t *testing.T, term *tuitest.Terminal, row int, word string) {
	t.Helper()
	line := term.Screen().Line(row)
	col := strings.Index(line, word)
	if row < 0 || col < 0 {
		t.Fatalf("ASSERTION: %q is not on row %d\n%s", word, row, term.Snapshot())
	}
	mouseClick(t, term, len([]rune(line[:col]))+1, row, tuitest.MouseLeft, 0)
}

// waitUnplaced waits for the host to show picture img nowhere.
func waitUnplaced(t *testing.T, term *tuitest.Terminal, host *lockedBuffer, img uint32, why string) {
	t.Helper()
	if err := term.WaitFor(func(tuitest.Screen) bool { return countPlaced([]byte(host.String()), img) == 0 }, uiTimeout); err != nil {
		t.Fatalf("ASSERTION: %s, and the picture is still placed: %+v", why, livePlaces([]byte(host.String())))
	}
}

// lrBackEdgeAnswer is a reply with a flowchart that runs across: six boxes
// and a back edge. Drawn as one row it is a little wider than the thread at
// 120 columns.
const lrBackEdgeAnswer = "The loop:\n\n" +
	"```mermaid\nflowchart LR\n  A[Read issue] --> B[Write test] --> C[Fix bug] --> D[Run suite] --> E[Open PR] --> F[Merge] --> G[Deploy]\n  D -->|fails| C\n```\n"

// TestCockpitThreadMermaidLRStaysAcross draws a flowchart LR of seven boxes
// and a back edge as text in the thread at 120 columns. It fits across once
// the gaps close up, so it keeps running across: the first and the last box
// are on one row, and the back edge's label shows. Turned to run down, it
// took over thirty rows with one box a row. The artifact is the frame
// mermaid-lr-across.
func TestCockpitThreadMermaidLRStaysAcross(t *testing.T) {
	_, term := openTextThreadOn(t, lrBackEdgeAnswer, "mermaid = \"text\"\n", nil, "The loop:")
	waitText(t, term, "the chart's first box", "Read issue")
	saveCockpitFrames(t, term, "mermaid-lr-across")
	railLists(t, term, "the chart is drawn across, as text", []string{"Read issue", "Deploy", "fails"}, []string{"flowchart LR"})
	s := term.Screen()
	if first, last := rowWith(s, "Read issue"), rowWith(s, "Deploy"); first != last {
		t.Fatalf("ASSERTION: the chart runs down: its first box is on row %d and its last on row %d\n%s", first, last, term.Snapshot())
	}
	alive(t, term, "after the LR chart")
}
