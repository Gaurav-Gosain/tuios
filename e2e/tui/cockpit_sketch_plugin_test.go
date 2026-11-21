package tuie2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuitest"
)

const timelineAnswer = "The history:\n\n```mermaid\n" + rareDiagram + "```\n\nThat is all.\n"

const fakeSVG = `<svg xmlns="http://www.w3.org/2000/svg" class="pele pele-timeline" viewBox="0 0 240 80" width="240" height="80" style=""><rect x="10" y="10" width="220" height="60" rx="6" fill="var(--pele-accent)"/></svg>`

func sketchPluginFolder(t *testing.T, base string) string {
	t.Helper()
	plug := t.TempDir()
	manifest, err := os.ReadFile(filepath.Join("..", "..", "plugins", "sketch", "tuios-plugin.toml"))
	if err != nil {
		t.Fatal(err)
	}
	writeFiles(t, plug, map[string]string{"tuios-plugin.toml": string(manifest)})
	if out, err := tuiosCLI(t, base, "plugins", "install", plug, "--yes", "--no-build"); err != nil {
		t.Fatalf("install the plugin: %v\n%s", err, out)
	}
	return plug
}

func giveSketchProgram(t *testing.T, plug string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(plug, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\ncat >/dev/null\ncase \"$*\" in\n*cells*) echo '" + fakeCells + "' ;;\n*svg*) echo '" + fakeSVG + "' ;;\n*) exit 2 ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(plug, "bin", "sketch"), []byte(script), 0o755); err != nil { //nolint:gosec // a program the test runs
		t.Fatal(err)
	}
}

func reopenAgentsView(t *testing.T, term *tuitest.Terminal, want ...string) {
	t.Helper()
	ckPress(t, term, tuitest.Esc)
	if strings.Contains(term.Screen().Text(), "Search agents") {
		ckPress(t, term, tuitest.Esc)
	}
	waitUntil(t, "Escape closes the agents view", uiTimeout, func() (bool, string) {
		return !strings.Contains(term.Screen().Text(), "Search agents"), term.Snapshot()
	})
	ckPress(t, term, tuitest.Ctrl('b'), "i")
	waitText(t, term, "the thread", want...)
	_ = term.WaitStable(uiTimeout)
}

// TestCockpitThreadSketchPluginText draws a timeline block in the thread as
// text. With no plugin, the block shows its source under a note.
// TestSketchPlugin checks the notice. With the plugin on and no program, the
// note says to build it. With the program, the next opening of the agents
// view draws the block with it, and no note shows. The artifacts are the
// frames sketch-thread-none, sketch-thread-no-program and
// sketch-thread-drawn.
func TestCockpitThreadSketchPluginText(t *testing.T) {
	base, term := openTextThreadOn(t, timelineAnswer, "mermaid = \"text\"\n", nil, "The history:")
	railLists(t, term, "the block shows its source under a note",
		[]string{"the sketch plugin draws timeline diagrams", "2002 : LinkedIn"}, []string{"PLUGIN-DREW"})
	saveCockpitFrames(t, term, "sketch-thread-none")

	plug := sketchPluginFolder(t, base)
	reopenAgentsView(t, term, "The history:")
	railLists(t, term, "the note says the plugin has no program",
		[]string{"has no program", "2002 : LinkedIn"}, []string{"PLUGIN-DREW", "draws timeline diagrams"})
	saveCockpitFrames(t, term, "sketch-thread-no-program")

	giveSketchProgram(t, plug)
	reopenAgentsView(t, term, "The history:")
	railLists(t, term, "the plugin draws the block", []string{"PLUGIN-DREW", "That is all."},
		[]string{"Cannot draw the diagram", "2002 : LinkedIn", "to draw this diagram"})
	saveCockpitFrames(t, term, "sketch-thread-drawn")
	alive(t, term, "after the plugin drew the block")
}

// TestCockpitThreadSketchPluginPicture draws a timeline block as a picture
// on a host with kitty graphics, the default of [agents] mermaid there. With
// no plugin the block keeps its text, the source under a note, and no
// picture reaches the host. With the plugin's program the picture comes from
// its SVG and is placed under the block's header, in place of the text. The
// artifacts are the frames sketch-picture-none and sketch-picture-drawn.
func TestCockpitThreadSketchPluginPicture(t *testing.T) {
	host := &lockedBuffer{}
	base, term := openTextThreadOn(t, timelineAnswer, "", host, "The history:")
	railLists(t, term, "the block shows its source under a note",
		[]string{"the sketch plugin draws timeline diagrams", "2002 : LinkedIn"}, nil)
	saveCockpitFrames(t, term, "sketch-picture-none")
	if n := len(livePlaces([]byte(host.String()))); n != 0 {
		t.Fatalf("ASSERTION: no plugin, and %d pictures are placed: %+v", n, livePlaces([]byte(host.String())))
	}

	giveSketchProgram(t, sketchPluginFolder(t, base))
	reopenAgentsView(t, term, "The history:")
	railLists(t, term, "the picture takes the place of the text", []string{"picture / text", "That is all."},
		[]string{"2002 : LinkedIn", "Cannot draw the diagram"})
	head := lastRowWith(term.Screen(), "picture / text")
	waitPlaced(t, term, host, func(p hostPlace) bool { return p.row == head+1 && p.rows > 0 })
	saveCockpitFrames(t, term, "sketch-picture-drawn", hostPictures(t, []byte(host.String()))...)
	alive(t, term, "after the plugin drew the picture")
}
