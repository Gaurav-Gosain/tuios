package agentproto

import (
	"strings"
	"testing"
)

func describe(ks []key) string {
	var parts []string
	for _, k := range ks {
		switch k.kind {
		case keyRune:
			parts = append(parts, string(k.r))
		case keyEnter:
			parts = append(parts, "<enter>")
		case keyBackspace:
			parts = append(parts, "<bs>")
		case keyCtrlC:
			parts = append(parts, "<c-c>")
		case keyCtrlD:
			parts = append(parts, "<c-d>")
		case keyCtrlU:
			parts = append(parts, "<c-u>")
		case keyPaste:
			parts = append(parts, "<paste "+k.text+">")
		}
	}
	return strings.Join(parts, " ")
}

func TestKeyParser(t *testing.T) {
	cases := []struct {
		name   string
		chunks []string
		want   string
	}{
		{"typing", []string{"hi\r"}, "h i <enter>"},
		{"a line feed submits too", []string{"a\n"}, "a <enter>"},
		{"controls", []string{"\x7f\x08\x03\x04\x15"}, "<bs> <bs> <c-c> <c-d> <c-u>"},
		{"arrows and function keys are dropped", []string{"\x1b[A\x1b[1;5C\x1bOP\x1b[15~x"}, "x"},
		{"alt and a key is dropped", []string{"\x1bxy"}, "y"},
		{"a paste then enter, the way tuios types a prompt", []string{"\x1b[200~line one\r\nline two\x1b[201~\r"}, "<paste line one\nline two> <enter>"},
		{"a paste split across reads", []string{"\x1b[20", "0~ab", "c\x1b[2", "01~"}, "<paste abc>"},
		{"control bytes inside a paste stay text until the end marker", []string{"\x1b[200~a\x03b\x1b[201~"}, "<paste a\x03b>"},
		{"utf8 split across reads", []string{"\xc3", "\xa9\xe2\x82", "\xac"}, "é €"},
		{"a tab is a space", []string{"\t"}, " "},
		{"a C1 control is dropped", []string{"\xc2\x9bz"}, "z"},
	}
	for _, tc := range cases {
		var p keyParser
		var got []key
		for _, c := range tc.chunks {
			got = append(got, p.feed([]byte(c))...)
		}
		if d := describe(got); d != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, d, tc.want)
		}
	}
}

// TestPasteIsBounded: a paste past maxPaste is cut there, and the parser
// still finds its end.
func TestPasteIsBounded(t *testing.T) {
	var p keyParser
	got := p.feed([]byte(pasteStart + strings.Repeat("x", maxPaste+100) + pasteEnd + "y"))
	if len(got) != 2 || got[0].kind != keyPaste || len(got[0].text) != maxPaste || got[1].r != 'y' {
		t.Fatalf("got %d keys, first paste of %d bytes", len(got), len(got[0].text))
	}
}
