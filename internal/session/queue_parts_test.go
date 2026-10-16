package session

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Gaurav-Gosain/tuios/internal/harness"
)

// These tests pin the queue's parts: the wire form the client sends, and the
// files the daemon writes for them, which must stay in the paste folder,
// never go through a link, and go on the sweep.

// TestQueuePartsWire: the parts as the client puts them on the wire, and every
// message the daemon must refuse before it queues anything.
func TestQueuePartsWire(t *testing.T) {
	var parts []queuePart
	wire := `[{"kind":"text","text":"this fails: "},{"kind":"paste","text":"line 1\nline 2\nline 3\nline 4\n","n":3},{"kind":"text","text":" and "},{"kind":"paste","text":"one long line"}]`
	if err := json.Unmarshal([]byte(wire), &parts); err != nil {
		t.Fatal(err)
	}
	shown, size, verr := checkQueueParts(parts)
	if verr != nil {
		t.Fatalf("checkQueueParts: %v", verr)
	}
	if want := "this fails: [Pasted text #3 · 4 lines] and [Pasted text #2 · 13 chars]"; shown != want {
		t.Errorf("shown = %q, want %q", shown, want)
	}
	if size != len("this fails: ")+len("line 1\nline 2\nline 3\nline 4\n")+len(" and ")+len("one long line") {
		t.Errorf("size = %d", size)
	}

	many := make([]queuePart, queueMaxParts+1)
	for i := range many {
		many[i] = queuePart{Kind: queuePartText, Text: "x"}
	}
	for name, bad := range map[string][]queuePart{
		"an unknown kind":         {{Kind: "video", Text: "x"}},
		"an image with text":      {{Kind: queuePartImage, Text: "x", Data: testPNG("a")}},
		"an image that is not":    {{Kind: queuePartImage, Data: []byte("#!/bin/sh\n")}},
		"text with data":          {{Kind: queuePartText, Text: "x", Data: testPNG("a")}},
		"nine images":             nineImages(),
		"an empty paste":          {{Kind: queuePartText, Text: "see"}, {Kind: queuePartPaste}},
		"no text":                 {{Kind: queuePartText, Text: "  \n"}},
		"too many parts":          many,
		"typed text over 16 KiB":  {{Kind: queuePartText, Text: strings.Repeat("a", queueMaxText+1)}},
		"a message over the cap":  {{Kind: queuePartPaste, Text: strings.Repeat("a", queueMessageMaxBytes+1)}},
		"labels past 16 KiB text": append([]queuePart{{Kind: queuePartText, Text: strings.Repeat("a", queueMaxText-200)}}, queuePart{Kind: queuePartPaste, Text: "x"}),
	} {
		if _, _, verr := checkQueueParts(bad); verr == nil || verr.Code != ErrVerbInvalidParams {
			t.Errorf("%s: checkQueueParts = %v, want invalid_params", name, verr)
		}
	}
}

// TestQueuedReplyFilesStayInThePasteFolder: a reply's file is the owner's
// alone, in the paste folder, never written through a link, and kept a day.
func TestQueuedReplyFilesStayInThePasteFolder(t *testing.T) {
	s, dir, now := newTestPasteStore(t)
	s.replyTTL = 24 * time.Hour
	data := []byte(strings.Repeat("pasted log line\n", 100))
	path, err := s.saveReply(data, ".txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != dir || !strings.HasPrefix(filepath.Base(path), pasteReplyPrefix) || filepath.Ext(path) != ".txt" {
		t.Errorf("the reply file is %s, want %s/%s*.txt", path, dir, pasteReplyPrefix)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(data) {
		t.Fatalf("the file holds %d bytes (%v), want the paste", len(got), err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != pasteFilePerm {
		t.Errorf("mode %v, want %v", info.Mode().Perm(), pasteFilePerm)
	}

	// Past an image's hour it stays. Past a day the sweep takes it.
	*now = now.Add(2 * time.Hour)
	s.sweep()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the sweep took a reply file after 2 hours: %v", err)
	}
	*now = now.Add(23 * time.Hour)
	s.sweep()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the sweep kept a reply file past a day: %v", err)
	}

	// A link where the folder goes is refused, and its target gets nothing.
	s2, dir2, _ := newTestPasteStore(t)
	target := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(dir2), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, dir2); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.saveReply(data, ".txt", nil); !errors.Is(err, errPasteDirLink) {
		t.Errorf("a reply file through a linked folder returned %v, want it refused", err)
	}
	if entries, _ := os.ReadDir(target); len(entries) != 0 {
		t.Errorf("the link's target got %d files", len(entries))
	}
}

// TestALargeQueuedPasteIsTypedAsAFile: a reply whose block takes it past
// 12 KiB is typed with the block's file named in its place. The file is
// written when the reply is typed, and holds the block's bytes.
func TestALargeQueuedPasteIsTypedAsAFile(t *testing.T) {
	d, _, sess, c, _, b := queueFixture(t)
	setAgentState(t, c, "work", b, "working", "", "")
	block := strings.Repeat("0123456789abcdef log line of the failing run\n", 100<<10/46)
	res := result(t, callP(c, t, "queue-prompt", map[string]any{"session": "work", "window": b, "parts": []map[string]any{
		{"kind": "text", "text": "echo see "},
		{"kind": "paste", "text": block, "n": 1},
	}}))
	if res["parts"] != float64(2) {
		t.Errorf("the reply says %v parts were read, want 2", res["parts"])
	}
	dir, err := d.pastes.dir()
	if err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("a file was written before the reply was typed: %v", entries)
	}
	setAgentState(t, c, "work", b, "idle", "", "")
	eventually(t, "the reply is typed with the file's label", 5*time.Second, func() bool {
		return paneShows(t, d, sess, b, "[Pasted text #1: ")
	})
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), pasteReplyPrefix) {
		t.Fatalf("the paste folder holds %v, want one reply file", entries)
	}
	got, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil || string(got) != block {
		t.Fatalf("the file holds %d bytes (%v), want the %d of the block", len(got), err, len(block))
	}
	if paneShows(t, d, sess, b, "log line of the failing run") {
		t.Error("the block's text was typed into the pane as well as its file")
	}
}

// TestALongMessageKeepsEveryFileItWrites: a message may hold more parts than
// the paste folder keeps files. The folder's cap must not delete a file of the
// message being written, or the agent finds the first paths gone.
func TestALongMessageKeepsEveryFileItWrites(t *testing.T) {
	s, _, _ := newTestPasteStore(t)
	held := map[string]bool{}
	var paths []string
	for i := range queueMaxParts {
		p, err := s.saveReply([]byte("block "+strings.Repeat("x", i)), ".txt", held)
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	if queueMaxParts <= pasteMaxFiles {
		t.Fatalf("queueMaxParts %d is within pasteMaxFiles %d: this test proves nothing", queueMaxParts, pasteMaxFiles)
	}
	for i, p := range paths {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("file %d of the message is gone before the agent read it: %v", i+1, err)
		}
	}
}

// TestPasteFilesOfTheSameSecondGoInTheOrderTheyWereWritten: the cap deletes
// the oldest file first. Files with the same modification time, which a
// coarse file system clock gives files written close together, go in the
// order they were written, not by the random letters in their names.
func TestPasteFilesOfTheSameSecondGoInTheOrderTheyWereWritten(t *testing.T) {
	s, dir, _ := newTestPasteStore(t)
	s.maxFiles = 3
	// Every file already on disk gets the same time, later than any new
	// file's, so the cap can tell them apart by nothing but their names.
	same := time.Now().Add(time.Hour)
	var paths []string
	for i := range 10 {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if err := os.Chtimes(filepath.Join(dir, e.Name()), same, same); err != nil {
				t.Fatal(err)
			}
		}
		p, err := s.saveReply([]byte("block "+strings.Repeat("x", i)), ".txt", nil)
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, p)
	}
	for i, p := range paths {
		_, err := os.Stat(p)
		if kept := err == nil; kept != (i >= len(paths)-3) {
			t.Errorf("file %d of 10: kept = %v, want only the last 3 kept", i+1, kept)
		}
	}
}

// nineImages is one image more than a message takes.
func nineImages() []queuePart {
	out := make([]queuePart, queueMaxImages+1)
	for i := range out {
		out[i] = queuePart{Kind: queuePartImage, Data: testPNG(strings.Repeat("x", i))}
	}
	return out
}

// TestQueueImagePartWire: an image part as the client sends it, its bytes in
// base64, decodes to the bytes, and a message of only an image is valid.
func TestQueueImagePartWire(t *testing.T) {
	png := testPNG("pixels")
	wire, err := json.Marshal(map[string]any{"parts": []map[string]any{{"kind": "image", "data": png, "n": 2}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wire), `"data":"iVBORw0KGgo`) {
		t.Fatalf("the image is not base64 on the wire: %s", wire)
	}
	var p queuePromptParams
	if err := json.Unmarshal(wire, &p); err != nil {
		t.Fatal(err)
	}
	shown, size, verr := checkQueueParts(p.Parts)
	if verr != nil {
		t.Fatalf("checkQueueParts: %v", verr)
	}
	if string(p.Parts[0].Data) != string(png) || size != len(png) || shown != "[Image #2 · png]" {
		t.Errorf("decoded %d bytes, size %d, shown %q", len(p.Parts[0].Data), size, shown)
	}
}

// TestSubmitPastesTypesEachPasteAlone: a reply with an image is typed as the
// text before it, the path alone, and the text after it, each its own
// bracketed paste, and submitted once. An empty side sends no empty paste,
// which a harness could read as a clipboard image.
func TestSubmitPastesTypesEachPasteAlone(t *testing.T) {
	pane := &recordingPane{bracketed: true}
	if _, err := submitPastesStamped(context.Background(), pane, []string{"look at ", "/run/p/x.png", " and fix it"}, harness.DefaultInputProfile(), 5*time.Millisecond, 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	got, _ := pane.recorded()
	want := []string{"\x1b[200~look at \x1b[201~", "\x1b[200~/run/p/x.png\x1b[201~", "\x1b[200~ and fix it\x1b[201~", "\r"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("writes = %q, want %q", got, want)
	}
	pane = &recordingPane{bracketed: true}
	if _, err := submitPastesStamped(context.Background(), pane, []string{"", "/run/p/x.png", ""}, harness.DefaultInputProfile(), 5*time.Millisecond, 20*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if got, _ := pane.recorded(); strings.Join(got, "|") != "\x1b[200~/run/p/x.png\x1b[201~|\r" {
		t.Fatalf("an image alone wrote %q", got)
	}
}

// TestAQueuedImageIsWrittenWhenTyped: the image is written to the paste
// folder when the reply is typed, named by the type its bytes show, and the
// pane, whose harness takes a path in the text, reads its path there.
func TestAQueuedImageIsWrittenWhenTyped(t *testing.T) {
	d, _, sess, c, _, b := queueFixture(t)
	setAgentState(t, c, "work", b, "working", "", "")
	png := testPNG("screenshot")
	result(t, callP(c, t, "queue-prompt", map[string]any{"session": "work", "window": b, "parts": []map[string]any{
		{"kind": "text", "text": "echo see "},
		{"kind": "image", "data": png, "n": 1},
	}}))
	dir, err := d.pastes.dir()
	if err != nil {
		t.Fatal(err)
	}
	setAgentState(t, c, "work", b, "idle", "", "")
	eventually(t, "the reply is typed with the image's path", 5*time.Second, func() bool {
		return paneShows(t, d, sess, b, "[Image #1] ")
	})
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), pasteReplyPrefix) || filepath.Ext(entries[0].Name()) != ".png" {
		t.Fatalf("the paste folder holds %v, want one .png reply file", entries)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, entries[0].Name())); string(got) != string(png) {
		t.Fatalf("the file holds %d bytes, want the image's %d", len(got), len(png))
	}
}
