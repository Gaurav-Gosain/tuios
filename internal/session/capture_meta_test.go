package session

import (
	"slices"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// TestPaneRevisionMovesWithOutputAndResize feeds the emulator through the
// writer a pane uses and checks the revision: output moves it, a resize moves
// it, a resize to the size it has does not, and it never goes back.
func TestPaneRevisionMovesWithOutputAndResize(t *testing.T) {
	p := &PTY{terminal: vt.NewWithScrollback(20, 4, 100), vtWriteChan: make(chan vtChunk, 8)}
	done := make(chan struct{})
	go func() { p.vtWriter(); close(done) }()
	p.vtWriteChan <- vtChunk{data: []byte("one\r\n"), seq: 5}
	p.vtWriteChan <- vtChunk{width: 20, height: 4} // the size it has
	p.vtWriteChan <- vtChunk{width: 30, height: 4}
	p.vtWriteChan <- vtChunk{data: []byte("1\r\n2\r\n3\r\n4\r\n5\r\n"), seq: 15}
	close(p.vtWriteChan)
	<-done
	m := p.Meta()
	if m.Revision != 16 {
		t.Errorf("revision = %d, want 15 bytes plus 1 resize", m.Revision)
	}
	if m.HistoryRows != p.terminal.ScrollbackLen() || m.HistoryRows == 0 {
		t.Errorf("history_rows = %d, scrollback holds %d", m.HistoryRows, p.terminal.ScrollbackLen())
	}
	content, cm := p.CaptureContentMeta(true, false)
	if cm != m {
		t.Errorf("CaptureContentMeta meta = %+v, want %+v", cm, m)
	}
	if got := len(splitCaptureLines(content)) - len(splitCaptureLines(p.CaptureContent(false, false))); got != m.HistoryRows {
		t.Errorf("a recent capture has %d lines more than the screen, history_rows says %d", got, m.HistoryRows)
	}
}

// splitCaptureLines splits captured text into its rows.
func splitCaptureLines(s string) []string {
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// TestListKeysIsTheParserGrammar checks list-keys against the parser: every
// name and alias it lists parses to that key, every modifier spelling it
// lists is taken, and every key the parser knows is listed.
func TestListKeysIsTheParserGrammar(t *testing.T) {
	kl := keyList()
	keys := kl["keys"].([]map[string]any)
	var names []string
	for _, k := range keys {
		name := k["name"].(string)
		names = append(names, name)
		for _, spelling := range append([]string{name}, k["aliases"].([]string)...) {
			got, err := parseKeyToken(spelling)
			if err != nil || got.named == nil {
				t.Errorf("%q, listed for %s, does not parse as a named key: %v", spelling, name, err)
				continue
			}
			if want := name; got.named.name != want && !(name == "BTab" && got.canonical == "shift+Tab") {
				t.Errorf("%q parses as %s, listed for %s", spelling, got.named.name, name)
			}
		}
	}
	if !slices.Equal(names, KeyNames()) {
		t.Errorf("list-keys names %v, the parser knows %v", names, KeyNames())
	}
	for _, m := range keyModifiers {
		for _, sp := range m.Spellings {
			tok := sp + "a"
			if sp == "^" {
				tok = "^A"
			}
			got, err := parseKeyToken(tok)
			if err != nil {
				t.Errorf("modifier spelling %q (%q) is refused: %v", sp, tok, err)
				continue
			}
			on := map[string]bool{"ctrl": got.mods.ctrl, "alt": got.mods.alt, "shift": got.mods.shift, "super": got.mods.super}
			if !on[m.Name] {
				t.Errorf("%q does not set %s: %+v", tok, m.Name, got.mods)
			}
		}
	}
	for _, ch := range ctrlCharacters {
		tok := "ctrl+" + ch
		if ch == "space" {
			tok = "ctrl+Space"
		}
		if _, err := parseKeyToken(tok); err != nil {
			t.Errorf("%q is refused: %v", tok, err)
		}
	}
}
