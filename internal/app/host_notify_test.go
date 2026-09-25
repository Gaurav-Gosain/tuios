package app

import (
	"strings"
	"testing"
)

// TestNotifySequenceWrapsForTheOuterTerminal pins the exact bytes for each
// outer terminal. tmux forwards no OSC 9 of its own, so a wrong wrap means the
// notification is eaten. screen stores a single ESC verbatim, so tmux's
// doubling would end the passthrough early and paint the rest on screen.
func TestNotifySequenceWrapsForTheOuterTerminal(t *testing.T) {
	for _, tc := range []struct {
		name  string
		outer outerMultiplexer
		text  string
		want  string
	}{
		{"none", outerNone, "build finished", "\x1b]9;build finished\x07"},
		{"tmux", outerTmux, "hi", "\x1bPtmux;\x1b\x1b]9;hi\x07\x1b\\"},
		{"screen", outerScreen, "hi", "\x1bP\x1b]9;hi\x07\x1b\\"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(hostNotifySequence(tc.text, tc.outer)); got != tc.want {
				t.Fatalf("sequence = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestNotifyPayloadCannotEscapeItsSequence is the injection guard. A pane title
// is attacker-influenced text (a shell prompt, a filename, an agent's message),
// and it is being handed to the user's real terminal.
func TestNotifyPayloadCannotEscapeItsSequence(t *testing.T) {
	nasty := "pane\x1b]0;pwned\x07 and \x1b[31m red \x9c more\nline\ttab"
	got := string(hostNotifySequence(nasty, outerNone))

	if strings.Count(got, "\x1b") != 1 {
		t.Errorf("payload carried an ESC through: %q", got)
	}
	if strings.Count(got, "\x07") != 1 {
		t.Errorf("payload carried a BEL through: %q", got)
	}
	if strings.ContainsRune(got, 0x9c) {
		t.Errorf("payload carried an ST through: %q", got)
	}
	if strings.ContainsAny(got[4:len(got)-1], "\n\t\r") {
		t.Errorf("payload carried a control character through: %q", got)
	}
}

// TestNotifyPayloadDoesNotStartWithACommandNumber guards the OSC 9 subcommand
// collision: a pane named "4" would otherwise send a progress-bar command.
func TestNotifyPayloadDoesNotStartWithACommandNumber(t *testing.T) {
	got := string(hostNotifySequence("4;50 finished", outerNone))
	if !strings.HasPrefix(got, "\x1b]9; ") {
		t.Fatalf("sequence = %q, want a space before the digits", got)
	}
	// A number that is not a command prefix is left alone.
	if got := string(hostNotifySequence("3 panes finished", outerNone)); got != "\x1b]9;3 panes finished\x07" {
		t.Fatalf("a bare leading digit was padded: %q", got)
	}
}

// TestNotifyPayloadIsCappedOnARuneBoundary keeps the payload near its limit,
// and keeps a cut multi-byte character from reaching the terminal as a lone
// continuation byte.
func TestNotifyPayloadIsCappedOnARuneBoundary(t *testing.T) {
	got := string(hostNotifySequence(strings.Repeat("x", 5000), outerNone))
	if len(got) > notifyTextLimit+16 {
		t.Fatalf("sequence is %d bytes, want the payload capped near %d", len(got), notifyTextLimit)
	}
	for i, r := range sanitizeNotifyText(strings.Repeat("é", notifyTextLimit)) {
		if r == 0xFFFD {
			t.Fatalf("truncation left an invalid rune at byte %d", i)
		}
	}
}
