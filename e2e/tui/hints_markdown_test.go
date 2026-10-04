package tuie2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

// TestHintsFindURLsInMarkdown is the README a maintainer opened in a pane:
// a markdown badge (an image inside a link) and the closing tag of an HTML
// block. The URL detector ran to the next space, so the badge was one hint
// covering both addresses and the bracket between them, and the "/p" of
// "</p>" was taken for a path.
//
// It reads the labels off the host's grid with hints open, then types the
// label of each badge URL and checks the clipboard holds exactly that URL.
//
// Negative controls, confirmed red (see NEGATIVE_CONTROLS.md): the build
// before the change fails on the "/" of "</p>"; with the ")" and "]" cases
// cut from urlEnd, the first badge URL copies with the rest of the line
// glued on; with the rooted-path check made false, "</p>" draws a label on
// its "/". The Wikipedia URL, which keeps its brackets, is the positive half.
func TestHintsFindURLsInMarkdown(t *testing.T) {
	const (
		badgeImage = "https://ko-fi.com/img/githubbutton_sm.svg"
		badgeLink  = "https://ko-fi.com/B0B81N8V1R"
		wikiURL    = "https://en.wikipedia.org/wiki/Go_(programming_language)"
	)
	out := &lockedBuffer{}
	term, base := startHintsIn(t, false, "", out)

	readme := filepath.Join(base, "readme.txt")
	body := "[![ko-fi](" + badgeImage + ")](" + badgeLink + ")\n" +
		"</p>\n" +
		"see [Go](" + wikiURL + ").\n" +
		"MD-READY\n"
	if err := os.WriteFile(readme, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	runInShell(t, term, "cat "+readme+" ; echo MD\"\"-SHOWN", "MD-SHOWN", shellTimeout)
	if err := term.WaitStable(uiTimeout); err != nil {
		t.Fatalf("the screen never settled: %v\n%s", err, term.Snapshot())
	}
	s := term.Screen()
	at := map[string][2]int{}
	for _, want := range []string{badgeImage, badgeLink, wikiURL, "</p>"} {
		c, r, ok := findLastOnGrid(s, want)
		if !ok {
			t.Fatalf("%q is not on screen:\n%s", want, term.Snapshot())
		}
		at[want] = [2]int{c, r}
	}

	for _, want := range []string{badgeImage, badgeLink, wikiURL} {
		openHints(t, term)
		pos := at[want]
		label := waitHintLabel(t, term, pos[0], pos[1], want)
		frame := term.Screen()

		// "</p>" keeps its text and carries no label.
		tag := at["</p>"]
		for i := range 4 {
			if isHintLabelCell(frame.Cell(tag[0]+i, tag[1])) {
				t.Fatalf("column %d of \"</p>\" is drawn as a hint label:\n%s", i, term.Snapshot())
			}
		}
		if want == badgeImage {
			saveArtifact(t, term, artifactDir(t), "hints-markdown")
		}

		from := len(clipboardWrites(out))
		if err := term.SendKeys(label); err != nil {
			t.Fatalf("type label %q: %v", label, err)
		}
		waitClipboardSequence(t, term, out, from, want)
		if err := term.WaitFor(func(s tuitest.Screen) bool { return hintsNoLabels(s) }, uiTimeout); err != nil {
			t.Fatalf("hints did not close after the copy: %v\n%s", err, term.Snapshot())
		}
	}
	alive(t, term, "after copying markdown URLs with hints")
}
