package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMailInPartsAttachesItsFiles: mail in parts that does not fit 8 KiB, or
// that holds an image, has each block and image written to the paste folder,
// attached, and named in the body. Mail that fits keeps its blocks in place
// and writes nothing.
func TestMailInPartsAttachesItsFiles(t *testing.T) {
	d, _, _, c, a, b := queueFixture(t)
	big := strings.Repeat("a line of the failing log\n", 600)
	res := result(t, callP(c, t, "send-agent-message", map[string]any{
		"session": "work", "from": a, "to": b,
		"parts": []map[string]any{
			{"kind": "text", "text": "see the log: "},
			{"kind": "paste", "text": big, "n": 1},
			{"kind": "image", "data": testPNG("shot"), "n": 1},
		},
	}))
	got := storedMessage(t, d, uint64(res["message_id"].(float64)))
	if !strings.HasPrefix(got.Text, "see the log: [Pasted text #1: ") || !strings.Contains(got.Text, "[Image #1: ") {
		t.Fatalf("the body is %q", got.Text)
	}
	dir, _ := d.pastes.dir()
	if len(got.Attachments) != 2 {
		t.Fatalf("attachments %+v, want the paste and the image", got.Attachments)
	}
	for _, att := range got.Attachments {
		if filepath.Dir(att.Path) != dir || !strings.HasPrefix(filepath.Base(att.Path), pasteReplyPrefix) {
			t.Errorf("attachment %s is not a reply file in the paste folder", att.Path)
		}
	}
	if data, _ := os.ReadFile(got.Attachments[0].Path); string(data) != big {
		t.Errorf("the paste's file holds %d bytes, want %d", len(data), len(big))
	}

	// Small: the block in its place, and no file.
	entries, _ := os.ReadDir(dir)
	before := len(entries)
	res = result(t, callP(c, t, "send-agent-message", map[string]any{
		"session": "work", "from": a, "to": b,
		"parts": []map[string]any{{"kind": "text", "text": "short: "}, {"kind": "paste", "text": "one\ntwo\nthree\nfour\n", "n": 1}},
	}))
	if mm := storedMessage(t, d, uint64(res["message_id"].(float64))); mm.Text != "short: one\ntwo\nthree\nfour\n" || len(mm.Attachments) != 0 {
		t.Errorf("small mail = %q with %d attachments", mm.Text, len(mm.Attachments))
	}
	if entries, _ := os.ReadDir(dir); len(entries) != before {
		t.Errorf("small mail wrote %d files", len(entries)-before)
	}
}

// storedMessage is the message the ring of session work holds under id.
func storedMessage(t *testing.T, d *Daemon, id uint64) AgentMessage {
	t.Helper()
	d.agents.mu.Lock()
	defer d.agents.mu.Unlock()
	m := d.agents.box("work").find(id)
	if m == nil {
		t.Fatalf("no message %d in the ring", id)
	}
	return *m
}
