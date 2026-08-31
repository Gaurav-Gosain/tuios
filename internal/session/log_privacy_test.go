package session

import (
	"strings"
	"testing"
)

// TestDebugPayloadRedactsTitleBelowVerbose is item 5's claim. A window title is
// rewritten by the shell on every prompt and carries the working directory, so
// it is content and content starts at verbose.
func TestDebugPayloadRedactsTitleBelowVerbose(t *testing.T) {
	codec := DefaultCodec()
	msg, err := NewMessageWithCodec(MsgCreatePTY, &CreatePTYPayload{
		Title:  "zsh ~/work/acme-secret-merger",
		Width:  80,
		Height: 24,
	}, codec)
	if err != nil {
		t.Fatalf("build message: %v", err)
	}

	for _, level := range []DebugLevel{DebugOff, DebugErrors, DebugBasic, DebugMessages} {
		got := debugPayloadAt(level, msg, codec)
		if strings.Contains(got, "acme-secret-merger") {
			t.Fatalf("level %s printed the window title: %s", level, got)
		}
		if !strings.Contains(got, "<29 chars>") {
			t.Fatalf("level %s did not report the title length: %s", level, got)
		}
	}

	if got := debugPayloadAt(DebugVerbose, msg, codec); !strings.Contains(got, "acme-secret-merger") {
		t.Fatalf("verbose must still capture content, got %s", got)
	}
}

// TestPTYCreatedRedactsTitleBelowVerbose covers the second title site, which the
// daemon sends back on every pane it opens.
func TestPTYCreatedRedactsTitleBelowVerbose(t *testing.T) {
	codec := DefaultCodec()
	msg, err := NewMessageWithCodec(MsgPTYCreated, &PTYCreatedPayload{
		ID:    "3f2a91c4-0000-0000-0000-000000000000",
		Title: "nvim /home/ada/notes.md",
	}, codec)
	if err != nil {
		t.Fatalf("build message: %v", err)
	}

	if got := debugPayloadAt(DebugMessages, msg, codec); strings.Contains(got, "/home/ada") {
		t.Fatalf("messages level printed a path from a title: %s", got)
	}
	if got := debugPayloadAt(DebugTrace, msg, codec); !strings.Contains(got, "/home/ada") {
		t.Fatalf("trace must still capture content, got %s", got)
	}
}

// TestRaisingToVerboseWarns checks the notice the level boundary owes the
// caller: raising the level starts recording content, and the log says so in
// the same place the caller will read the capture back from.
func TestRaisingToVerboseWarns(t *testing.T) {
	restoreLevel(t, DebugOff)
	ClearLogBuffer()

	SetDebugLevel(DebugVerbose)

	entries := GetLogEntries(0)
	if len(entries) == 0 {
		t.Fatal("raising the level to verbose logged nothing")
	}
	last := entries[len(entries)-1].Message
	if !strings.Contains(last, "records pane content, window titles and paths") {
		t.Fatalf("no content warning on raising the level, got %q", last)
	}

	// Lowering must not warn, and neither must a level at or below messages.
	ClearLogBuffer()
	SetDebugLevel(DebugMessages)
	for _, e := range GetLogEntries(0) {
		if strings.Contains(e.Message, "records pane content") {
			t.Fatalf("lowering the level warned: %q", e.Message)
		}
	}
}
