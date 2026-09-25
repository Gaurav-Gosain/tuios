package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

// TestStashPutPrintsOnlyThePathOnStdout holds the one output contract the
// command has. The skill tells an agent to run path=$(tuios stash put f) and pass
// the result to --attach, so anything else on stdout would end up in the path.
// The note goes to the second writer, which is stderr.
func TestStashPutPrintsOnlyThePathOnStdout(t *testing.T) {
	raw := json.RawMessage(`{"path":"/run/user/1000/tuios/stash/s1/abc.png","bytes":2048,"deduped":false,"evicted":0,"evictions":0}`)
	var out, notes bytes.Buffer
	if err := printStashPut(&out, &notes, raw); err != nil {
		t.Fatalf("printStashPut: %v", err)
	}
	if got := out.String(); got != "/run/user/1000/tuios/stash/s1/abc.png\n" {
		t.Fatalf("stdout is %q, want only the stored path", got)
	}
	if !strings.Contains(notes.String(), "2.0 KB") {
		t.Errorf("the note does not say the size: %q", notes.String())
	}
}

// TestStashGetPrintsOnlyThePathOnStdout keeps stash get to the same contract as
// stash put: the written path alone on stdout, the note on stderr.
func TestStashGetPrintsOnlyThePathOnStdout(t *testing.T) {
	var out, notes bytes.Buffer
	printStashGet(&out, &notes, "flame.png", 2048, " on build")
	if got := out.String(); got != "flame.png\n" {
		t.Fatalf("stdout is %q, want only the written path", got)
	}
	if !strings.Contains(notes.String(), "copied 2.0 KB on build") {
		t.Errorf("the note is %q", notes.String())
	}
}
