package vt_test

import (
	"bytes"
	"encoding/base64"
	"reflect"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// FuzzKittyHeader holds ParseKittyHeader to ParseKittyCommand on everything
// but the decoded payload. The ways it could go wrong:
//   - a control key read differently, or the echo check given a different
//     payload, so the daemon answers a query or refuses animation differently;
//   - PayloadErr left unset where it decides a reply, so a guest that sent a
//     bad payload gets no EINVAL from the daemon, or set where the full parse
//     leaves it unset;
//   - a reply built where the full parse builds none.
func FuzzKittyHeader(f *testing.F) {
	for _, s := range []string{
		"a=t,f=24,s=1,v=1,i=41;AA!A",
		"a=q,f=24,s=1,v=1,t=d,i=42;AA!A",
		"a=q,f=24,s=1,v=1,t=d,i=43;AAAA",
		"a=t,q=2,i=44;AA!A",
		"m=0;AA!A",
		"I=5;A",
		"i=3;EINVAL:bad",
		"i=3,a=T;EINVAL:bad",
		"a=T,t=f,i=9;L3RtcC94",
		"a=T,t=s,i=9;!!",
		"a=q;" + strings.Repeat("QUJD", 100) + "=",
		"a=d,d=a",
		";",
		"a=a,i=1,c=2;",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		full, errFull := vt.ParseKittyCommand(data)
		head, errHead := vt.ParseKittyHeader(data)
		if (errFull == nil) != (errHead == nil) || (full == nil) != (head == nil) {
			t.Fatalf("%q: full parse (%v, %v), header parse (%v, %v)", data, full, errFull, head, errHead)
		}
		if full == nil {
			return
		}
		if !bytes.Equal(vt.KittyPayloadErrorResponse(full), vt.KittyPayloadErrorResponse(head)) {
			t.Fatalf("%q: the full parse replies %q, the header parse %q", data,
				vt.KittyPayloadErrorResponse(full), vt.KittyPayloadErrorResponse(head))
		}
		if vt.IsKittyEchoedResponse(full) != vt.IsKittyEchoedResponse(head) {
			t.Fatalf("%q: the two parses disagree on whether it is an echoed reply", data)
		}
		if head.PayloadErr != nil && full.PayloadErr == nil {
			t.Fatalf("%q: the header parse found a payload error the full parse did not: %v", data, head.PayloadErr)
		}
		if head.Data != nil {
			t.Fatalf("%q: the header parse decoded the image data", data)
		}
		if head.FilePath != full.FilePath && (head.FilePath != "" || namesObject(full)) {
			t.Fatalf("%q: the header parse named %q, the full parse %q", data, head.FilePath, full.FilePath)
		}
		// Everything else is the same.
		a, b := *full, *head
		a.Data, a.FilePath, a.RawPayload, a.PayloadErr = nil, "", "", nil
		b.FilePath, b.RawPayload, b.PayloadErr = "", "", nil
		if !reflect.DeepEqual(a, b) { //nolint:govet // PayloadErr is cleared on both sides above, so no error is compared
			t.Fatalf("%q: the control keys differ:\n full   %+v\n header %+v", data, a, b)
		}
	})
}

// namesObject reports whether the header parse must name the object of cmd:
// a shared memory or temp file transmission whose name decodes.
func namesObject(cmd *vt.KittyCommand) bool {
	return (cmd.Medium == vt.KittyMediumSharedMemory || cmd.Medium == vt.KittyMediumTempFile) &&
		len(cmd.RawPayload) <= 8192
}

// TestKittyHeaderNamesTheObject is the boundary the daemon's shared memory
// cleanup depends on. The ways it could fail:
//   - the header parse leaves FilePath empty for t=s, so the daemon has no name
//     to delete and every unread frame stays in /dev/shm;
//   - the same for t=t, so unread temp files stay on disk;
//   - the name decoded for a medium whose payload is image data (t=d), which
//     throws away the saving the header parse exists for;
//   - a q=2 command (no reply owed) skipped, which is how a video stream sends.
func TestKittyHeaderNamesTheObject(t *testing.T) {
	enc := func(s string) string { return base64.RawStdEncoding.EncodeToString([]byte(s)) }
	for _, tc := range []struct {
		body, want string
	}{
		{"a=T,t=s,f=32,s=4,v=4,q=2;" + enc("/tuios-shm-1"), "/tuios-shm-1"},
		{"a=t,t=s,i=7;" + enc("shm-frame"), "shm-frame"},
		{"a=T,t=t,f=100,q=2;" + enc("/tmp/tty-graphics-protocol-x.png"), "/tmp/tty-graphics-protocol-x.png"},
		{"a=T,t=d,f=24,s=1,v=1,q=2;" + enc("abc"), ""},
		{"a=T,t=f,q=2;" + enc("/home/u/pic.png"), ""},
	} {
		cmd, err := vt.ParseKittyHeader([]byte(tc.body))
		if err != nil {
			t.Fatalf("%q: %v", tc.body, err)
		}
		if cmd.FilePath != tc.want {
			t.Errorf("%q: header parse named %q, want %q", tc.body, cmd.FilePath, tc.want)
		}
		if cmd.Data != nil {
			t.Errorf("%q: header parse decoded image data", tc.body)
		}
	}
}

// kittyStreamChunk is one 4096-byte chunk from the middle of a kitty graphics
// stream sent with q=2: the shape of nearly every APC a compositor writes.
func kittyStreamChunk() []byte {
	px := bytes.Repeat([]byte{1, 2, 3, 255}, 1024)
	enc := base64.StdEncoding.EncodeToString(px)[:4096]
	return []byte("q=2,m=1;" + enc)
}

func BenchmarkParseKittyCommandChunk(b *testing.B) {
	chunk := kittyStreamChunk()
	b.SetBytes(int64(len(chunk)))
	b.ReportAllocs()
	for b.Loop() {
		_, _ = vt.ParseKittyCommand(chunk)
	}
}

func BenchmarkParseKittyHeaderChunk(b *testing.B) {
	chunk := kittyStreamChunk()
	b.SetBytes(int64(len(chunk)))
	b.ReportAllocs()
	for b.Loop() {
		_, _ = vt.ParseKittyHeader(chunk)
	}
}

// TestKittyHeaderLeavesThePayloadAlone is the budget: parsing the control keys
// of a 4 KB stream chunk allocates no copy of its payload. The daemon decoded
// every chunk of every frame and kept the text twice, about 7 KB per chunk,
// only to throw it all away.
func TestKittyHeaderLeavesThePayloadAlone(t *testing.T) {
	res := testing.Benchmark(BenchmarkParseKittyHeaderChunk)
	t.Logf("a 4 KB stream chunk: %d bytes allocated by the header parse", res.AllocedBytesPerOp())
	if got := res.AllocedBytesPerOp(); got > 512 {
		t.Errorf("parsing the control keys of a 4 KB chunk allocated %d bytes, want at most 512", got)
	}
}
