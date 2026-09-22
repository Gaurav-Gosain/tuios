package vt

import (
	"bytes"
	"encoding/base64"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/ansi/parser"
)

// TestSeqParserGrowsToTheCap pins the buffer's shape: it starts small, a
// payload that fits the cap arrives whole, and a payload past the cap is cut
// at the cap, as the fixed 4 MiB buffer cut it.
func TestSeqParserGrowsToTheCap(t *testing.T) {
	const cap = 64 * 1024
	p := newSeqParser(cap)
	if got := len(p.data); got != seqParserInitialData {
		t.Fatalf("a fresh parser holds %d bytes of data buffer, want %d", got, seqParserInitialData)
	}
	var got []byte
	p.SetHandler(ansi.Handler{HandleOsc: func(_ int, data []byte) { got = append(got[:0], data...) }})
	feed := func(s string) {
		for i := range len(s) {
			p.Advance(s[i])
		}
	}

	payload := strings.Repeat("x", cap-len("52;c;"))
	feed("\x1b]52;c;" + payload + "\x1b\\")
	want := "52;c;" + payload
	if string(got) != want {
		t.Fatalf("a payload that fits the cap arrived as %d bytes, want %d", len(got), len(want))
	}
	if len(p.data) != cap {
		t.Fatalf("the buffer grew to %d, want the cap %d", len(p.data), cap)
	}

	feed("\x1b]52;c;" + strings.Repeat("y", 3*cap) + "\x1b\\")
	if len(got) != cap {
		t.Fatalf("a payload past the cap arrived as %d bytes, want it cut at %d", len(got), cap)
	}
	if !bytes.HasPrefix(got, []byte("52;c;yyy")) {
		t.Fatalf("the cut payload does not start with the sequence: %q", got[:16])
	}
	if len(p.data) != cap {
		t.Fatalf("the buffer is %d after a payload past the cap, want the cap %d", len(p.data), cap)
	}

	feed("\x1b]0;title\x1b\\")
	if string(got) != "0;title" {
		t.Fatalf("a short OSC after a long one arrived as %q", got)
	}
}

// TestSeqParserCsiParamsMatchUpstream feeds the same random CSI-heavy input
// to this parser and to upstream's and requires the same dispatches, action by
// action. The CSI parameter bytes take a direct path in advance that skips the
// transition table, and this is what holds it to the table's result: digits,
// ':' and ';' in any mix, sub-parameters, missing parameters, and more
// parameters than the buffer holds.
func TestSeqParserCsiParamsMatchUpstream(t *testing.T) {
	type dispatch struct {
		cmd    ansi.Cmd
		params string
	}
	record := func(out *[]dispatch) ansi.Handler {
		return ansi.Handler{
			HandleCsi: func(cmd ansi.Cmd, params ansi.Params) {
				var b strings.Builder
				for _, p := range params {
					b.WriteString(strconv.Itoa(p.Param(-1)))
					if p.HasMore() {
						b.WriteByte(':')
					} else {
						b.WriteByte(';')
					}
				}
				*out = append(*out, dispatch{cmd, b.String()})
			},
		}
	}

	alphabet := []byte("0123456789;;;::\x1b[[m?<>= $")
	rng := rand.New(rand.NewPCG(1, 2))
	for round := range 2000 {
		var in []byte
		for range 1 + rng.IntN(8) {
			in = append(in, "\x1b["...)
			for range rng.IntN(120) {
				in = append(in, alphabet[rng.IntN(len(alphabet))])
			}
			in = append(in, "mHJ"[rng.IntN(3)])
		}

		var got, want []dispatch
		ours := newSeqParser(1024)
		ours.SetHandler(record(&got))
		theirs := ansi.NewParser()
		theirs.SetParamsSize(parser.MaxParamsSize)
		theirs.SetHandler(record(&want))
		for i, b := range in {
			if a, w := ours.Advance(b), theirs.Advance(b); a != w {
				t.Fatalf("round %d byte %d (%q) of %q: action %d, upstream %d", round, i, b, in, a, w)
			}
			if ours.State() != theirs.State() {
				t.Fatalf("round %d byte %d (%q) of %q: state %d, upstream %d", round, i, b, in, ours.State(), theirs.State())
			}
		}
		if !slices.Equal(got, want) {
			t.Fatalf("round %d, input %q:\n got  %v\n want %v", round, in, got, want)
		}
	}
}

// TestEmulatorKeepsLargePayloads holds the emulator to the cap the fixed
// buffer had: an OSC 52 write of a few MiB reaches the clipboard whole.
func TestEmulatorKeepsLargePayloads(t *testing.T) {
	e := NewEmulator(80, 24)
	var got int
	e.SetCallbacks(Callbacks{ClipboardSet: func(_, content string) { got = len(content) }})
	raw := bytes.Repeat([]byte("A"), 2<<20)
	encoded := base64.StdEncoding.EncodeToString(raw)
	if _, err := e.Write([]byte("\x1b]52;c;" + encoded + "\x1b\\")); err != nil {
		t.Fatal(err)
	}
	if got != len(raw) {
		t.Fatalf("a %d byte OSC 52 payload reached the clipboard as %d bytes", len(raw), got)
	}
}
