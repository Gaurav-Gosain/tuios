package session

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// verbLineSeeds are request lines the daemon accepts on its socket, plus the
// malformed and hostile shapes an untrusted client can send. Anything that can
// open the socket can send these, so the decoder has to reject them cheaply.
// FuzzVerbDispatch (fuzz_verb_test.go) runs them through the dispatcher.
var verbLineSeeds = []string{
	`{"id":1,"verb":"list-verbs","params":{}}`,
	`{"id":1,"verb":"list-sessions"}`,
	`{"verb":"new-session","params":{"name":"work"}}`,
	`{"id":"str-id","verb":"list-windows","params":{"session":"work"}}`,
	`{"id":null,"verb":"kill-session","params":{"session":"gone"}}`,
	// Missing and empty fields.
	`{}`,
	`{"verb":""}`,
	`{"id":1}`,
	`{"params":{}}`,
	// Wrong types for each field.
	`{"verb":123}`,
	`{"verb":["a"]}`,
	`{"verb":{"a":1}}`,
	`{"params":"not an object"}`,
	`{"id":{"nested":{"deep":1}}}`,
	// Malformed JSON.
	``,
	`{`,
	`}`,
	`[`,
	`null`,
	`true`,
	`0`,
	`"bare string"`,
	`{"verb":"list-verbs",}`,
	`{"verb":"list-verbs"` + "\x00",
	// Duplicate and unknown keys.
	`{"verb":"a","verb":"b"}`,
	`{"verb":"list-verbs","unknown":1,"another":2}`,
	// Unknown verbs, which route into the did-you-mean hint path.
	`{"verb":"list-verb"}`,
	`{"verb":"lst-sessions"}`,
	`{"verb":"nonsense"}`,
	`{"verb":"` + strings.Repeat("a", 65536) + `"}`,
	`{"verb":"` + strings.Repeat("list-verbs", 4096) + `"}`,
	// Deep nesting in params, which the handler unmarshals lazily.
	`{"verb":"list-verbs","params":` + strings.Repeat("[", 1024) + strings.Repeat("]", 1024) + `}`,
	`{"verb":"list-verbs","params":{"a":` + strings.Repeat(`{"a":`, 512) + `1` + strings.Repeat(`}`, 512) + `}}`,
	// Numbers that do not fit.
	`{"id":99999999999999999999999999,"verb":"list-verbs"}`,
	`{"verb":"list-verbs","params":{"n":1e999}}`,
	// Non-UTF-8 and control bytes.
	"{\"verb\":\"\xff\xfe\"}",
	"{\"verb\":\"a\\ud800\"}",
	"{\"verb\":\"\\u0000\"}",
}

// FuzzClosestMatch drives the suggestion path directly, including the edit
// distance computation, against the real verb list.
func FuzzClosestMatch(f *testing.F) {
	f.Add("")
	f.Add("list-verbs")
	f.Add("list-verb")
	f.Add("LIST-VERBS")
	f.Add("nonsense")
	f.Add("\xff\xfe")
	f.Add(strings.Repeat("a", 4096))
	f.Add(strings.Repeat("世", 4096))

	known := knownVerbNames()

	f.Fuzz(func(t *testing.T, target string) {
		if len(target) > 16*1024*1024 {
			target = target[:16*1024*1024]
		}

		done := make(chan string, 1)
		go func() { done <- closestMatch(target, known) }()

		select {
		case got := <-done:
			if got != "" && got == target {
				t.Fatalf("closestMatch(%q) suggested its own input", target)
			}
			if got != "" {
				if _, ok := verbRegistry[got]; !ok {
					t.Fatalf("closestMatch(%q) suggested unregistered verb %q", target, got)
				}
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("closestMatch did not return within 10s for a %d-byte target", len(target))
		}
	})
}

// FuzzReadMessageFraming drives the binary frame reader over arbitrary bytes.
// The daemon reads this straight off the socket, so a length prefix is fully
// attacker-controlled and the 16 MiB cap is the only thing between a client and
// an unbounded allocation.
func FuzzReadMessageFraming(f *testing.F) {
	frame := func(totalLen uint32, body ...byte) []byte {
		b := []byte{
			byte(totalLen >> 24), byte(totalLen >> 16),
			byte(totalLen >> 8), byte(totalLen),
		}
		return append(b, body...)
	}

	f.Add(frame(2, byte(MsgPTYOutput), wireCodecGob))
	f.Add(frame(6, byte(MsgInput), 1, 'a', 'b', 'c', 'd')) // codec byte 1: the reserved JSON value
	// Truncated at every boundary.
	f.Add([]byte{})
	f.Add([]byte{0})
	f.Add([]byte{0, 0, 0})
	f.Add(frame(2))
	f.Add(frame(100, byte(MsgInput)))
	// Below the minimum frame.
	f.Add(frame(0))
	f.Add(frame(1, byte(MsgInput)))
	// Exactly at, just under and just over the 16 MiB cap, with no body: the
	// reader must reject the oversized ones without allocating for them.
	f.Add(frame(16*1024*1024, byte(MsgInput), wireCodecGob))
	f.Add(frame(16*1024*1024-1, byte(MsgInput), wireCodecGob))
	f.Add(frame(16*1024*1024+1, byte(MsgInput), wireCodecGob))
	f.Add(frame(0xFFFFFFFF, byte(MsgInput), wireCodecGob))
	// A well-formed PTY frame and a truncated one.
	f.Add(frame(2+36+4, byte(MsgPTYOutput), wireCodecGob,
		'0', '1', '2', '3', '4', '5', '6', '7', '8', '9',
		'a', 'b', 'c', 'd', 'e', 'f', 'g', 'h', 'i', 'j',
		'k', 'l', 'm', 'n', 'o', 'p', 'q', 'r', 's', 't',
		'u', 'v', 'w', 'x', 'y', 'z', 'd', 'a', 't', 'a'))
	f.Add(frame(2+36, byte(MsgPTYOutput), wireCodecGob))
	// A PTY frame whose id is all padding, and one whose codec byte is not
	// the one a writer sends.
	f.Add(append(frame(2+36+2, byte(MsgInput), wireCodecGob), append(make([]byte, 36), 'h', 'i')...))
	f.Add(frame(3, byte(MsgResize), 1, 'x'))

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			data = data[:1<<20]
		}

		r := bytes.NewReader(data)
		// A single reader can hold several frames; a desync on one frame must
		// not turn into an unbounded read loop on the rest.
		for range 64 {
			off := len(data) - r.Len()
			msg, err := ReadMessage(r)
			if err != nil {
				break
			}
			if msg == nil {
				t.Fatalf("ReadMessage returned a nil message and no error")
			}
			// The writer must put back exactly the frame the reader took,
			// apart from the codec byte, which the reader ignores and the
			// writer always sends as gob. A frame that re-encodes to other
			// bytes is one a relaying daemon forwards changed.
			consumed := append([]byte(nil), data[off:len(data)-r.Len()]...)
			consumed[5] = wireCodecGob
			var back bytes.Buffer
			if err := WriteMessage(&back, msg); err != nil {
				t.Fatalf("WriteMessage of a frame ReadMessage accepted: %v", err)
			}
			if !bytes.Equal(back.Bytes(), consumed) {
				t.Fatalf("frame re-encodes differently:\nread  %x\nwrote %x", consumed, back.Bytes())
			}
			// The reader accepted the frame, so it was within the cap and the
			// body was fully present.
			if len(msg.Payload) > 16*1024*1024 {
				t.Fatalf("accepted a payload of %d bytes, over the 16 MiB cap",
					len(msg.Payload))
			}
			// The payload can never exceed what the input actually held.
			if len(msg.Payload) > len(data) {
				t.Fatalf("payload of %d bytes from %d bytes of input",
					len(msg.Payload), len(data))
			}
			// PTY frames are parsed further, with the ID taken from a fixed
			// 36-byte prefix.
			if msg.Type == MsgPTYOutput || msg.Type == MsgInput {
				ptyID, payload, perr := ParseBinaryPTYMessage(msg.Payload)
				if perr == nil {
					if len(ptyID) > 36 {
						t.Fatalf("ParseBinaryPTYMessage returned a %d-byte ID", len(ptyID))
					}
					if len(payload) > len(msg.Payload) {
						t.Fatalf("ParseBinaryPTYMessage returned more data than the payload held")
					}
					if strings.HasSuffix(ptyID, "\x00") {
						t.Fatalf("ParseBinaryPTYMessage left padding on the ID: %q", ptyID)
					}
					// And the PTY writer puts the same frame back.
					var pty bytes.Buffer
					if err := writePTYFrame(&pty, msg.Type, ptyID, payload); err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(pty.Bytes(), consumed) {
						t.Fatalf("PTY frame for %q re-encodes differently:\nread  %x\nwrote %x", ptyID, consumed, pty.Bytes())
					}
				}
			}
		}
	})
}
