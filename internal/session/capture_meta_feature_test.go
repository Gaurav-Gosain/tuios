//go:build !slim

package session

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestCapturePaneReportsHistoryAndRevision checks capture-pane and
// list-windows report history_rows and revision on a live pane, with the
// daemon's boot_id beside them.
func TestCapturePaneReportsHistoryAndRevision(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	d, sp := startTestDaemon(t)
	sess := makeSessionWithWindow(t, d, "meta")
	id := sess.GetState().Windows[0].ID
	c := dialVerb(t, sp)

	capture := func(n int, source string) map[string]any {
		return result(t, c.call(t, `{"id":`+strconv.Itoa(n)+`,"verb":"capture-pane","params":{"session":"meta","window":"`+id+`","source":"`+source+`"}}`))
	}
	// The quotes keep the marker out of the command line. The tty echoes text
	// sent before the shell reads it, and the shell draws it again after its
	// prompt, so a plain marker shows twice before seq has run.
	result(t, c.call(t, `{"id":1,"verb":"send-text","params":{"session":"meta","window":"`+id+`","text":"seq 1 60; echo DONE''-MARK\n"}}`))
	deadline := time.Now().Add(10 * time.Second)
	var first map[string]any
	for {
		first = capture(2, "recent")
		if strings.Contains(first["content"].(string), "\nDONE-MARK\n") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the loop never finished:\n%s", first["content"])
		}
		time.Sleep(50 * time.Millisecond)
	}
	// Let the prompt finish drawing, then read twice with nothing between.
	var a, b map[string]any
	for range 50 {
		time.Sleep(100 * time.Millisecond)
		a, b = capture(3, "visible"), capture(4, "visible")
		if a["revision"] == b["revision"] {
			break
		}
	}
	if a["revision"] != b["revision"] || a["content"] != b["content"] {
		t.Fatalf("two captures of an idle pane: revisions %v and %v", a["revision"], b["revision"])
	}
	hist := int(first["history_rows"].(float64))
	if hist < 30 {
		t.Errorf("history_rows = %d after 60 rows in a 24-row pane", hist)
	}
	recent, visible := capture(5, "recent"), capture(6, "visible")
	if got := len(splitCaptureLines(recent["content"].(string))) - len(splitCaptureLines(visible["content"].(string))); got != int(recent["history_rows"].(float64)) {
		t.Errorf("recent has %d lines more than visible, history_rows says %v", got, recent["history_rows"])
	}
	att := result(t, c.call(t, `{"id":7,"verb":"list-attention"}`))
	if a["boot_id"] == "" || a["boot_id"] != att["boot_id"] {
		t.Errorf("capture boot_id %v, list-attention boot_id %v", a["boot_id"], att["boot_id"])
	}

	rev := a["revision"].(float64)
	result(t, c.call(t, `{"id":8,"verb":"send-text","params":{"session":"meta","window":"`+id+`","text":"echo more\n"}}`))
	deadline = time.Now().Add(10 * time.Second)
	for capture(9, "visible")["revision"].(float64) <= rev {
		if time.Now().After(deadline) {
			t.Fatal("output never moved the revision")
		}
		time.Sleep(50 * time.Millisecond)
	}

	lw := result(t, c.call(t, `{"id":10,"verb":"list-windows","params":{"session":"meta"}}`))
	w := lw["windows"].([]any)[0].(map[string]any)
	if _, ok := w["history_rows"].(float64); !ok {
		t.Errorf("list-windows entry has no history_rows: %v", w)
	}
	if r, ok := w["revision"].(float64); !ok || r <= rev {
		t.Errorf("list-windows revision = %v, want past %v", w["revision"], rev)
	}
}
