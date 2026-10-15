package vt

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestDecodeKittyPayload pins the decoder against every padding shape a
// sender uses, and against the shapes that are not base64 at all.
func TestDecodeKittyPayload(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "empty", in: "", want: ""},
		{name: "whole groups, no padding needed", in: "MTIz", want: "123"},
		{name: "one byte, padded", in: "QQ==", want: "A"},
		{name: "one byte, unpadded", in: "QQ", want: "A"},
		{name: "two bytes, padded", in: "QUI=", want: "AB"},
		{name: "two bytes, unpadded", in: "QUI", want: "AB"},
		{name: "padded groups joined", in: "QQ==QUI=", want: "AAB"},
		{name: "padded group then unpadded tail", in: "QQ==QUI", want: "AAB"},
		{name: "one character left over", in: "QUJDR", wantErr: true},
		{name: "padding on a whole group", in: "MTIz=", wantErr: true},
		{name: "three padding characters", in: "Q===", wantErr: true},
		{name: "padding that does not complete a group", in: "QUJD=", wantErr: true},
		{name: "padding alone", in: "==", wantErr: true},
		{name: "not the base64 alphabet", in: "@@@@", wantErr: true},
		{name: "url-safe alphabet is not kitty's", in: "-_-_", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeKittyPayload([]byte(tt.in))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("DecodeKittyPayload(%q) = %q, want an error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("DecodeKittyPayload(%q): %v", tt.in, err)
			}
			if string(got) != tt.want {
				t.Fatalf("DecodeKittyPayload(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// testImage is bytes standing in for a PNG. Its length is not a multiple of
// three, so its base64 needs padding and an unpadded sender leaves it off.
func testImage(n int) []byte {
	img := make([]byte, n)
	for i := range img {
		img[i] = byte(i*7 + i/251)
	}
	return img
}

// chunkBase64 splits encoded text the way senders do: every chunk but the last
// a multiple of four characters.
func chunkBase64(encoded string, size int) []string {
	var chunks []string
	for len(encoded) > size {
		chunks = append(chunks, encoded[:size])
		encoded = encoded[size:]
	}
	return append(chunks, encoded)
}

// TestParseKittyCommandSenderShapes feeds the parser the exact command shapes
// kitten icat and chafa write, captured from their output, and checks the
// bytes that come out are the bytes that went in.
func TestParseKittyCommandSenderShapes(t *testing.T) {
	img := testImage(50318) // 50318 % 3 == 2
	raw := base64.RawStdEncoding.EncodeToString(img)
	padded := base64.StdEncoding.EncodeToString(img)
	if raw == padded {
		t.Fatal("the test image needs padding, or the unpadded case tests nothing")
	}

	// kitten icat, stream mode: a=T with the geometry and m=1 on the first
	// chunk of 131072 characters, then a bare "a=T,q=2" final chunk, all of it
	// from Go's RawStdEncoding, so the final chunk has no padding.
	icatChunks := chunkBase64(raw, 4096*8)
	var icatCmds []string
	for i, c := range icatChunks {
		switch {
		case i == 0:
			icatCmds = append(icatCmds, "a=T,q=2,f=100,m=1,s=301,v=211,X=4;"+c)
		case i < len(icatChunks)-1:
			icatCmds = append(icatCmds, "a=T,q=2,m=1;"+c)
		default:
			icatCmds = append(icatCmds, "a=T,q=2;"+c)
		}
	}

	// chafa: a params-only first chunk, then m=1 continuations of 4096
	// characters and an m=0 final chunk, padded.
	chafaCmds := []string{"a=T,f=100,s=301,v=211,c=30,r=10,m=1"}
	chafaChunks := chunkBase64(padded, 4096)
	for i, c := range chafaChunks {
		more := "1"
		if i == len(chafaChunks)-1 {
			more = "0"
		}
		chafaCmds = append(chafaCmds, "m="+more+";"+c)
	}

	// icat in one command, the shape it uses for an image small enough.
	icatSingle := []string{"a=T,q=2,f=100,s=7,v=5,X=1;" + raw}

	for _, tc := range []struct {
		name string
		cmds []string
	}{
		{"icat stream, chunked, unpadded", icatCmds},
		{"icat single command, unpadded", icatSingle},
		{"chafa, chunked, padded", chafaCmds},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got []byte
			for i, c := range tc.cmds {
				cmd, err := ParseKittyCommand([]byte(c))
				if err != nil || cmd == nil {
					t.Fatalf("chunk %d: ParseKittyCommand: %v", i, err)
				}
				if cmd.PayloadErr != nil {
					t.Fatalf("chunk %d: payload refused: %v", i, cmd.PayloadErr)
				}
				got = append(got, cmd.Data...)
			}
			if !bytes.Equal(got, img) {
				t.Fatalf("decoded %d bytes, want the %d sent (first difference at %d)",
					len(got), len(img), firstDiff(got, img))
			}
		})
	}
}

func firstDiff(a, b []byte) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return min(len(a), len(b))
}

// TestParseKittyCommandFileMedia checks the path a file, temp file or shared
// memory command names comes out whole, padded or not. kitten icat's paths are
// unpadded, and a path whose length is not a multiple of three used to come
// out empty, so the image was silently never drawn.
func TestParseKittyCommandFileMedia(t *testing.T) {
	paths := []string{
		"/tmp/a.png",         // 10 bytes: needs two '='
		"/tmp/ab.png",        // 11 bytes: needs one '='
		"/tmp/abc.png",       // 12 bytes: needs none
		"icat-AB7TAIZ4DG2QI", // a shared memory name icat uses
		"/var/folders/w6/hzb0jzqd647bzm2v_kg5k6k80000gn/T/tty-graphics-protocol-545021856",
	}
	for _, medium := range []string{"f", "t", "s"} {
		for _, path := range paths {
			for _, enc := range []*base64.Encoding{base64.RawStdEncoding, base64.StdEncoding} {
				payload := enc.EncodeToString([]byte(path))
				name := fmt.Sprintf("t=%s %s %q", medium, payload, path)
				t.Run(name, func(t *testing.T) {
					cmd, err := ParseKittyCommand([]byte("a=T,q=2,f=100,t=" + medium + ",s=200,v=120;" + payload))
					if err != nil || cmd == nil {
						t.Fatalf("ParseKittyCommand: %v", err)
					}
					if cmd.PayloadErr != nil {
						t.Fatalf("payload refused: %v", cmd.PayloadErr)
					}
					if cmd.FilePath != path {
						t.Fatalf("FilePath = %q, want %q", cmd.FilePath, path)
					}
					if cmd.Data != nil {
						t.Fatalf("a file medium carried Data %q", cmd.Data)
					}
				})
			}
		}
	}
}

// TestParseKittyCommandUndecodable checks that text that is not base64 is
// never handed on as image data, which is what the parser used to do.
func TestParseKittyCommandUndecodable(t *testing.T) {
	for _, c := range []string{
		"a=T,f=100,i=7;@@@@",
		"a=t,f=100,t=f,i=7;@@@@",
		"a=T,f=100,i=7;QUJDR",
	} {
		cmd, err := ParseKittyCommand([]byte(c))
		if err != nil || cmd == nil {
			t.Fatalf("%q: ParseKittyCommand: %v", c, err)
		}
		if cmd.PayloadErr == nil {
			t.Errorf("%q: no PayloadErr", c)
		}
		if cmd.Data != nil || cmd.FilePath != "" {
			t.Errorf("%q: undecodable payload passed through as Data=%q FilePath=%q", c, cmd.Data, cmd.FilePath)
		}
		if cmd.RawPayload == "" {
			t.Errorf("%q: RawPayload lost; the echo check reads it", c)
		}
	}
}

// responseReader collects everything an emulator writes back to its guest.
// The response pipe blocks a writer until it is read, so it has to be read
// the whole time the test writes.
type responseReader struct {
	mu  sync.Mutex
	buf strings.Builder
}

func readResponses(e *Emulator) *responseReader {
	r := &responseReader{}
	go func() {
		p := make([]byte, 4096)
		for {
			n, err := e.Read(p)
			r.mu.Lock()
			r.buf.Write(p[:n])
			r.mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	return r
}

func (r *responseReader) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.String()
}

// waitFor returns what was read once it contains want, or fails the test.
func (r *responseReader) waitFor(t *testing.T, want string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if s := r.String(); strings.Contains(s, want) {
			return s
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("never answered %q; got %q", want, r.String())
	return ""
}

// TestKittyPayloadErrorAnswered drives the emulator with undecodable payloads
// and checks the guest is told, in the cases kitty tells it, and only then.
func TestKittyPayloadErrorAnswered(t *testing.T) {
	const einval = "EINVAL:payload is not valid base64"
	tests := []struct {
		name string
		apc  string
		want string // "" means no reply at all
	}{
		{"transmit with an id", "a=T,f=100,i=7;@@@@", "\x1b_Gi=7;" + einval + "\x1b\\"},
		{"transmit with q=1 still hears an error", "a=T,f=100,i=7,q=1;@@@@", "\x1b_Gi=7;" + einval + "\x1b\\"},
		{"transmit with q=2 hears nothing", "a=T,f=100,i=7,q=2;@@@@", ""},
		{"transmit without an id hears nothing", "a=T,f=100;@@@@", ""},
		{"file transmit with an id", "a=T,t=f,i=9;QUJDR", "\x1b_Gi=9;" + einval + "\x1b\\"},
		{"query is answered EINVAL, not OK", "a=q,i=31,s=1,v=1,f=24;@@@@", "\x1b_Gi=31;" + einval + "\x1b\\"},
		{"query without an id is still answered", "a=q,s=1,v=1,f=24;@@@@", "\x1b_G" + einval + "\x1b\\"},
		{"an echoed error is not answered", "i=42;EINVAL:bad params", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, withPassthrough := range []bool{false, true} {
				e := NewEmulator(80, 24)
				var passed []*KittyCommand
				if withPassthrough {
					e.SetKittyPassthroughFunc(func(cmd *KittyCommand, _ []byte) {
						passed = append(passed, cmd)
					})
				}
				r := readResponses(e)
				if _, err := e.Write([]byte("\x1b_G" + tt.apc + "\x1b\\")); err != nil {
					t.Fatalf("Write: %v", err)
				}
				// A marker behind the APC, so "nothing was answered" is
				// asserted after the emulator has certainly processed it.
				if _, err := e.Write([]byte("\x1b[c")); err != nil {
					t.Fatalf("Write: %v", err)
				}
				r.waitFor(t, "\x1b[?")
				got := r.String()
				got = got[:strings.LastIndex(got, "\x1b[?")]
				if got != tt.want {
					t.Errorf("passthrough=%v: reply %q, want %q", withPassthrough, got, tt.want)
				}
				if withPassthrough && strings.HasPrefix(tt.apc, "a=q") && len(passed) > 0 {
					t.Errorf("an undecodable query reached the passthrough, which would answer it OK")
				}
				for _, cmd := range passed {
					if cmd.Data != nil || cmd.FilePath != "" {
						t.Errorf("passthrough was handed undecoded text as Data=%q FilePath=%q", cmd.Data, cmd.FilePath)
					}
				}
			}
		})
	}
}

// TestKittyIcatProbeBatchAnswered sends the probe batch kitten icat opens
// with, byte for byte, and checks every question in it is answered, in order,
// with DA1 last. icat reads until the DA1 reply, so a graphics answer that
// comes after it is one it never sees. The XTWINOPS size reports are in the
// batch too: icat reads pixel sizes from the tty, but other image tools ask.
func TestKittyIcatProbeBatchAnswered(t *testing.T) {
	e := NewEmulator(80, 24)
	e.SetCellSize(10, 20)
	r := readResponses(e)

	batch := "\x1b_Ga=q,f=24,s=1,v=1,S=3,i=1;MTIz\x1b\\" +
		"\x1b_Ga=q,f=24,t=t,s=1,v=1,S=86,i=2;L3Zhci9mb2xkZXJzL3c2L2h6YjBqenFkNjQ3YnptMnZfa2c1azZrODAwMDBnbi9UL2tpdHR5LXR0eS1ncmFwaGljcy1wcm90b2NvbC01NDUwMjE4NTY\x1b\\" +
		"\x1b_Ga=q,f=24,t=s,s=1,v=1,S=18,i=3;aWNhdC1KWkhHTDJDNlhNQVFP\x1b\\" +
		"\x1b[14t\x1b[16t\x1b[18t" +
		"\x1b[c"
	if _, err := e.Write([]byte(batch)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := r.waitFor(t, "\x1b[?")
	want := []string{
		"\x1b_Gi=1;OK\x1b\\",
		"\x1b_Gi=2;OK\x1b\\",
		"\x1b_Gi=3;OK\x1b\\",
		"\x1b[4;480;800t",
		"\x1b[6;20;10t",
		"\x1b[8;24;80t",
		"\x1b[?",
	}
	at := 0
	for _, w := range want {
		i := strings.Index(got[at:], w)
		if i < 0 {
			t.Fatalf("reply %q missing or out of order in %q", w, got)
		}
		at += i + len(w)
	}
}
