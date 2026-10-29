package dnd_test

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/dnd"
	"tuios.dev/vt"
)

// The wire layer of the drag and drop protocol is checked the way the OSC
// corpus is: against the exact bytes the real programs write. Each input
// below is a sequence captured from yazi 26.9.1 or kitten 0.49.0 running in a
// pty, or a reply kitty 0.49 writes (kitty/dnd.c), so a reader that passes
// here reads what those programs send and a writer that passes here writes
// what they read.

func TestDnDParseRealSequences(t *testing.T) {
	tests := []struct {
		name string
		body string
		want dnd.Command
	}{
		{
			// yazi, at startup.
			name: "yazi offers drags",
			body: "72;t=o:x=1;",
			want: dnd.Command{Type: 'o', X: 1, Payload: []byte{}},
		},
		{
			name: "yazi accepts drops",
			body: "72;t=a;text/uri-list",
			want: dnd.Command{Type: 'a', Payload: []byte("text/uri-list")},
		},
		{
			// The kitten writes m=0 even where it means nothing.
			name: "kitten accepts drops",
			body: "72;t=a:m=0;text/uri-list",
			want: dnd.Command{Type: 'a', Payload: []byte("text/uri-list")},
		},
		{
			name: "kitten sends its machine id",
			body: "72;t=a:x=1:m=0;1:b1202f578751d483bdeaee5142871e6a44e252394acac6d98191b578eeeefb25",
			want: dnd.Command{Type: 'a', X: 1, Payload: []byte("1:b1202f578751d483bdeaee5142871e6a44e252394acac6d98191b578eeeefb25")},
		},
		{
			name: "yazi stops accepting",
			body: "72;t=A",
			want: dnd.Command{Type: 'A', Payload: []byte{}},
		},
		{
			// kitty, to a program it offered a drag to.
			name: "kitty tells the program the drag gesture",
			body: "72;t=o:x=3:y=4:X=31:Y=90",
			want: dnd.Command{Type: 'o', X: 3, Y: 4, PX: 31, PY: 90, Payload: []byte{}},
		},
		{
			name: "kitty says the drag left the window",
			body: "72;t=m:x=-1:y=-1",
			want: dnd.Command{Type: 'm', X: -1, Y: -1, Payload: []byte{}},
		},
		{
			// The kitten's text drag image: base64 text, 6 by 1 units.
			name: "kitten presents a text icon",
			body: "72;t=p:x=-1:X=6:Y=1:m=0;8J+TgQ==",
			want: dnd.Command{Type: 'p', X: -1, PX: 6, PY: 1, Payload: []byte("8J+TgQ==")},
		},
		{
			name: "a multiplexer id is kept",
			body: "72;t=a:i=7;text/plain",
			want: dnd.Command{Type: 'a', ID: 7, Payload: []byte("text/plain")},
		},
		{
			// The kitten's chunks past the first carry only m.
			name: "a continuation chunk has no type",
			body: "72;m=1;AAAA",
			want: dnd.Command{More: true, Payload: []byte("AAAA")},
		},
		{
			name: "the number may be left off",
			body: "t=q",
			want: dnd.Command{Type: 'q', Payload: []byte{}},
		},
		{
			// A key from a later version of the protocol is skipped, as kitty
			// skips it, rather than the message refused.
			name: "an unknown key is skipped",
			body: "72;t=a:z=5;text/plain",
			want: dnd.Command{Type: 'a', Payload: []byte("text/plain")},
		},
		{
			// A payload may hold a semicolon; only the first two split.
			name: "the payload keeps its semicolons",
			body: "72;t=E;EINVAL:a;b",
			want: dnd.Command{Type: 'E', Payload: []byte("EINVAL:a;b")},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := dnd.Parse([]byte(tc.body))
			if !ok {
				t.Fatalf("ParseDnD(%q) refused the message", tc.body)
			}
			if got.Type != tc.want.Type || got.More != tc.want.More || got.ID != tc.want.ID ||
				got.Op != tc.want.Op || got.X != tc.want.X || got.Y != tc.want.Y ||
				got.PX != tc.want.PX || got.PY != tc.want.PY || !bytes.Equal(got.Payload, tc.want.Payload) {
				t.Errorf("ParseDnD(%q)\n got %+v\nwant %+v", tc.body, got, tc.want)
			}
		})
	}
}

func TestDnDParseRefuses(t *testing.T) {
	tests := []struct {
		name, body string
	}{
		{"a bare metadata part", "72;t=a:foo;text/plain"},
		{"a type longer than one character", "72;t=ab"},
		{"a type the protocol does not name", "72;t=z"},
		{"a value that is not a number", "72;t=m:x=abc"},
		{"a number past 32 bits", "72;t=m:x=4294967296"},
		{"an empty value", "72;t=m:x="},
		{"a payload past one chunk", "72;t=p:x=0;" + strings.Repeat("A", dnd.MaxChunk+1)},
		{"another OSC's body", "52;c;aGVsbG8="},
		{"the number alone", "72"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if cmd, ok := dnd.Parse([]byte(tc.body)); ok {
				t.Errorf("ParseDnD(%q) took the message as %+v", tc.body, cmd)
			}
		})
	}
}

// TestDnDAssemblerChains puts chunked messages back together the way kitty's
// screen does: the first chunk's metadata carries through, a list comes out
// whole, a stream comes out chunk by chunk.
func TestDnDAssemblerChains(t *testing.T) {
	feed := func(t *testing.T, a *dnd.Assembler, bodies ...string) []dnd.Command {
		t.Helper()
		var out []dnd.Command
		for _, b := range bodies {
			cmd, ok := dnd.Parse([]byte(b))
			if !ok {
				t.Fatalf("ParseDnD(%q) refused", b)
			}
			if done, ok := a.Add(cmd); ok {
				out = append(out, done)
			}
		}
		return out
	}

	t.Run("a MIME list split across chunks comes out whole", func(t *testing.T) {
		var a dnd.Assembler
		got := feed(t, &a, "72;t=a:m=1;text/uri-list ", "72;m=1;text/plain ", "72;m=0;image/png")
		if len(got) != 1 {
			t.Fatalf("got %d messages, want 1: %+v", len(got), got)
		}
		if got[0].Type != 'a' || string(got[0].Payload) != "text/uri-list text/plain image/png" || got[0].More {
			t.Errorf("got %+v", got[0])
		}
	})

	t.Run("a data stream comes out chunk by chunk with the first chunk's keys", func(t *testing.T) {
		var a dnd.Assembler
		got := feed(t, &a, "72;t=p:x=0:m=1;AAAA", "72;m=1;BBBB", "72;m=0;CCCC", "72;t=p:x=0")
		if len(got) != 4 {
			t.Fatalf("got %d messages, want 4: %+v", len(got), got)
		}
		for i, want := range []string{"AAAA", "BBBB", "CCCC", ""} {
			if got[i].Type != 'p' || got[i].X != 0 || string(got[i].Payload) != want {
				t.Errorf("chunk %d: got %+v, want payload %q with t=p x=0", i, got[i], want)
			}
		}
		if !got[0].More || !got[1].More || got[2].More || got[3].More {
			t.Errorf("More flags: %v %v %v %v", got[0].More, got[1].More, got[2].More, got[3].More)
		}
	})

	t.Run("a query in the middle of a chain passes, and the chain goes on", func(t *testing.T) {
		var a dnd.Assembler
		got := feed(t, &a, "72;t=a:m=1;text/uri-list ", "72;t=q", "72;m=0;text/plain")
		if len(got) != 2 || got[0].Type != 'q' || got[1].Type != 'a' || string(got[1].Payload) != "text/uri-list text/plain" {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("a continuation with no chain is dropped", func(t *testing.T) {
		var a dnd.Assembler
		if got := feed(t, &a, "72;m=0;AAAA"); len(got) != 0 {
			t.Errorf("got %+v, want nothing", got)
		}
	})

	t.Run("a message without m is complete on its own", func(t *testing.T) {
		var a dnd.Assembler
		got := feed(t, &a, "72;t=o:x=1;", "72;t=a;text/uri-list")
		if len(got) != 2 || got[0].Type != 'o' || got[1].Type != 'a' {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("a list past the cap is dropped whole", func(t *testing.T) {
		var a dnd.Assembler
		big := strings.Repeat("a", dnd.MaxChunk)
		var bodies []string
		for range dnd.MaxListBytes/dnd.MaxChunk + 1 {
			bodies = append(bodies, "72;t=a:m=1;"+big)
		}
		bodies = append(bodies, "72;m=0;x")
		if got := feed(t, &a, bodies...); len(got) != 0 {
			t.Errorf("got %d messages, want none", len(got))
		}
		// The next message is read as new.
		if got := feed(t, &a, "72;t=A"); len(got) != 1 || got[0].Type != 'A' {
			t.Errorf("after the dropped chain: got %+v", got)
		}
	})
}

// TestDnDEncodeKittyForm checks the writer against the bytes kitty writes
// (kitty/dnd.c, send_payload_to_child and the senders that call it).
func TestDnDEncodeKittyForm(t *testing.T) {
	tests := []struct {
		name string
		cmd  dnd.Command
		want string
	}{
		{
			name: "the query reply",
			cmd:  dnd.Command{Type: 'q'},
			want: "\x1b]72;t=q\x1b\\",
		},
		{
			name: "the query reply echoes the multiplexer id",
			cmd:  dnd.Command{Type: 'q', ID: 3},
			want: "\x1b]72;t=q:i=3\x1b\\",
		},
		{
			name: "the drag left the window",
			cmd:  dnd.Command{Type: 'm', X: -1, Y: -1},
			want: "\x1b]72;t=m:x=-1:y=-1\x1b\\",
		},
		{
			name: "a move carries the MIME list, and its keys at zero",
			cmd:  dnd.Command{Type: 'm', X: 2, Y: 5, Op: 3, Payload: []byte("text/uri-list")},
			want: "\x1b]72;t=m:o=3:x=2:y=5:X=0:Y=0:m=0;text/uri-list\x1b\\",
		},
		{
			name: "the drag gesture",
			cmd:  dnd.Command{Type: 'o', X: 3, Y: 4, PX: 31, PY: 90},
			want: "\x1b]72;t=o:x=3:y=4:X=31:Y=90\x1b\\",
		},
		{
			name: "an error",
			cmd:  dnd.Command{Type: 'R', X: 1, Payload: dnd.ErrorPayload("EPERM", "cannot drop into self window")},
			want: "\x1b]72;t=R:x=1:y=0:m=0;EPERM:cannot drop into self window\x1b\\",
		},
		{
			// yazi reads t=e:x=4 only with its y, and t=e:x=1 only with its
			// y, which is 0 for the first MIME type. kitty writes both.
			name: "a finished drag carries y=0",
			cmd:  dnd.Command{Type: 'e', X: 4},
			want: "\x1b]72;t=e:x=4:y=0\x1b\\",
		},
		{
			name: "an accepted first MIME type carries y=0",
			cmd:  dnd.Command{Type: 'e', X: 1, Y: 0},
			want: "\x1b]72;t=e:x=1:y=0\x1b\\",
		},
		{
			name: "an operation change to none carries o=0",
			cmd:  dnd.Command{Type: 'e', X: 2, Op: 0},
			want: "\x1b]72;t=e:o=0:x=2:y=0\x1b\\",
		},
		{
			name: "a gesture at the origin carries its cell",
			cmd:  dnd.Command{Type: 'o', X: 0, Y: 0},
			want: "\x1b]72;t=o:x=0:y=0:X=0:Y=0\x1b\\",
		},
		{
			name: "a registration stays short",
			cmd:  dnd.Command{Type: 'o', X: 1},
			want: "\x1b]72;t=o:x=1:y=0\x1b\\",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(tc.cmd.Encode()); got != tc.want {
				t.Errorf("Encode()\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

// TestDnDEncodeDataStream checks a data response against kitty's form: 3072
// raw bytes per chunk, m=1 on every chunk but the last with data, and the
// empty closing message. The stream read back through ParseDnD and the
// assembler decodes to the data, which is the round trip yazi and the kitten
// make.
func TestDnDEncodeDataStream(t *testing.T) {
	data := bytes.Repeat([]byte("0123456789abcdef"), 400) // 6400 bytes: three chunks
	out := dnd.EncodeData(dnd.Command{Type: 'r', X: 1}, data)
	seqs := strings.Split(strings.TrimSuffix(string(out), "\x1b\\"), "\x1b\\")
	if len(seqs) != 4 {
		t.Fatalf("got %d sequences, want 3 data chunks and the close:\n%q", len(seqs), out)
	}
	if !strings.HasPrefix(seqs[0], "\x1b]72;t=r:x=1:y=0:m=1;") || !strings.HasPrefix(seqs[1], "\x1b]72;t=r:x=1:y=0:m=1;") ||
		!strings.HasPrefix(seqs[2], "\x1b]72;t=r:x=1:y=0:m=0;") || seqs[3] != "\x1b]72;t=r:x=1:y=0" {
		t.Errorf("chunk headers:\n%q\n%q\n%q\n%q", seqs[0][:20], seqs[1][:20], seqs[2][:20], seqs[3])
	}
	if n := len(seqs[0]) - len("\x1b]72;t=r:x=1:y=0:m=1;"); n != base64.StdEncoding.EncodedLen(3072) {
		t.Errorf("first chunk carries %d encoded bytes, want %d", n, base64.StdEncoding.EncodedLen(3072))
	}

	var a dnd.Assembler
	var got []byte
	ended := false
	for _, s := range seqs {
		cmd, ok := dnd.Parse([]byte(strings.TrimPrefix(s, "\x1b]")))
		if !ok {
			t.Fatalf("ParseDnD refused %q", s)
		}
		chunk, ok := a.Add(cmd)
		if !ok {
			t.Fatalf("assembler held back %q", s)
		}
		if chunk.Type != 'r' || chunk.X != 1 {
			t.Errorf("chunk keys %+v", chunk)
		}
		if len(chunk.Payload) == 0 && !chunk.More {
			ended = true
			break
		}
		dec, err := dnd.DecodePayload(chunk.Payload)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		got = append(got, dec...)
	}
	if !ended || !bytes.Equal(got, data) {
		t.Errorf("round trip: ended=%v, %d bytes back, want %d", ended, len(got), len(data))
	}

	if got := string(dnd.EncodeData(dnd.Command{Type: 'r', X: 2}, nil)); got != "\x1b]72;t=r:x=2:y=0\x1b\\" {
		t.Errorf("empty data: got %q", got)
	}
}

// TestDnDReachesPrivateOSC: an OSC 72 written to the emulator comes out of
// Callbacks.PrivateOSC with its body whole, for both terminators, and parses.
// The emulator must not interpret the number itself, or the host never sees
// it.
func TestDnDReachesPrivateOSC(t *testing.T) {
	for _, tc := range []struct{ name, in string }{
		{"ST", "\x1b]72;t=a;text/uri-list\x1b\\"},
		{"BEL", "\x1b]72;t=a;text/uri-list\x07"},
		{"split across writes", "\x1b]72;t=a;text/" + "\x00" + "uri-list\x1b\\"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			emu := vt.NewEmulator(20, 3)
			var got []dnd.Command
			emu.SetCallbacks(vt.Callbacks{PrivateOSC: func(cmd int, data []byte) {
				if cmd != dnd.Code {
					t.Errorf("PrivateOSC got number %d", cmd)
				}
				c, ok := dnd.Parse(data)
				if !ok {
					t.Errorf("ParseDnD refused %q", data)
				}
				got = append(got, c)
			}})
			for part := range strings.SplitSeq(tc.in, "\x00") {
				_, _ = emu.Write([]byte(part))
			}
			if len(got) != 1 || got[0].Type != 'a' || string(got[0].Payload) != "text/uri-list" {
				t.Errorf("got %+v", got)
			}
		})
	}
}

// FuzzDnDParse: no body panics the parser or the assembler, and every
// message the parser takes encodes to bytes the parser takes back with the
// same keys.
func FuzzDnDParse(f *testing.F) {
	for _, seed := range []string{
		"72;t=o:x=1;", "72;t=a;text/uri-list", "72;t=a:x=1:m=0;1:abc", "72;m=1;AAAA",
		"72;t=m:x=-1:y=-1", "72;t=p:x=-1:X=6:Y=1:m=0;8J+TgQ==", "72;t=r:x=1:y=2:Y=3", "t=q:i=9",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		cmd, ok := dnd.Parse(body)
		var a dnd.Assembler
		a.Add(cmd)
		a.Add(cmd)
		if !ok || cmd.Type == 0 {
			return
		}
		enc := cmd.Encode()
		again, ok := dnd.Parse(bytes.TrimSuffix(enc[2:], []byte("\x1b\\")))
		if !ok {
			t.Fatalf("Encode of %+v does not parse: %q", cmd, enc)
		}
		if again.Type != cmd.Type || again.ID != cmd.ID || again.Op != cmd.Op || again.X != cmd.X ||
			again.Y != cmd.Y || again.PX != cmd.PX || again.PY != cmd.PY || again.More != cmd.More {
			t.Fatalf("round trip changed the keys: %+v -> %+v", cmd, again)
		}
	})
}
