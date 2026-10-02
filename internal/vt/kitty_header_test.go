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
		if head.Data != nil || head.FilePath != "" {
			t.Fatalf("%q: the header parse decoded the payload", data)
		}
		// Everything else is the same.
		a, b := *full, *head
		a.Data, a.FilePath, a.RawPayload, a.PayloadErr = nil, "", "", nil
		b.RawPayload, b.PayloadErr = "", nil
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("%q: the control keys differ:\n full   %+v\n header %+v", data, a, b)
		}
	})
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
