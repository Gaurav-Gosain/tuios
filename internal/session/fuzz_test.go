package session

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// verbLineSeeds are request lines the daemon accepts on its socket, plus the
// malformed and hostile shapes an untrusted client can send. Anything that can
// open the socket can send these, so the decoder has to reject them cheaply.
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

// FuzzVerbRequestDecode drives the JSON decode and verb-lookup path that
// dispatchVerbLine runs on every line arriving from a socket client, stopping
// short of the handlers themselves (which need a live daemon).
//
// The lookup path is where an unknown verb turns into a did-you-mean hint, and
// that hint compares the client's string against every registered verb. The
// line limit is 16 MiB, so this has to stay cheap for a name of any length.
func FuzzVerbRequestDecode(f *testing.F) {
	for _, s := range verbLineSeeds {
		f.Add([]byte(s))
	}

	known := knownVerbNames()

	f.Fuzz(func(t *testing.T, line []byte) {
		// The daemon's scanner caps a line at 16 MiB; stay under it so the
		// target measures the decode path rather than the scanner.
		if len(line) > 16*1024*1024 {
			line = line[:16*1024*1024]
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			return
		}

		var req verbRequest
		if err := json.Unmarshal(line, &req); err != nil {
			// Malformed JSON is answered with an error envelope. That envelope
			// must still be serialisable, or the daemon cannot reply at all.
			resp := &verbResponse{
				Error: newVerbError(ErrVerbInvalidRequest, "malformed JSON request: "+err.Error()),
			}
			if _, merr := json.Marshal(resp); merr != nil {
				t.Fatalf("error envelope for a malformed line is not serialisable: %v", merr)
			}
			return
		}

		if req.Verb == "" {
			return
		}
		if _, ok := verbRegistry[req.Verb]; ok {
			return
		}

		// Unknown verb: this is the hint path. It must return within a budget
		// that does not scale with the client's string, since any client can
		// send a 16 MiB one and the daemon answers on the accept goroutine.
		done := make(chan string, 1)
		go func() { done <- closestMatch(req.Verb, known) }()

		var suggestion string
		select {
		case suggestion = <-done:
		case <-time.After(10 * time.Second):
			t.Fatalf("closestMatch did not return within 10s for a %d-byte verb", len(req.Verb))
		}

		// A suggestion is only useful if it names a verb that exists.
		if suggestion != "" {
			if _, ok := verbRegistry[suggestion]; !ok {
				t.Fatalf("closestMatch suggested %q, which is not a registered verb", suggestion)
			}
			if suggestion == req.Verb {
				t.Fatalf("closestMatch suggested the verb the client already sent")
			}
		}

		resp := &verbResponse{
			ID: req.ID,
			Error: hintedVerbError(ErrVerbUnknownVerb, "unknown verb "+req.Verb, &VerbHint{
				Verb:       "list-verbs",
				DidYouMean: suggestion,
				Available:  known,
			}),
		}
		if _, err := json.Marshal(resp); err != nil {
			t.Fatalf("unknown-verb envelope is not serialisable: %v", err)
		}
	})
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

	f.Add(frame(2, byte(MsgPTYOutput), byte(CodecGob)))
	f.Add(frame(6, byte(MsgInput), byte(CodecJSON), 'a', 'b', 'c', 'd'))
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
	f.Add(frame(16*1024*1024, byte(MsgInput), byte(CodecGob)))
	f.Add(frame(16*1024*1024-1, byte(MsgInput), byte(CodecGob)))
	f.Add(frame(16*1024*1024+1, byte(MsgInput), byte(CodecGob)))
	f.Add(frame(0xFFFFFFFF, byte(MsgInput), byte(CodecGob)))
	// A well-formed PTY frame and a truncated one.
	f.Add(frame(2+36+4, byte(MsgPTYOutput), byte(CodecGob),
		'0', '1', '2', '3', '4', '5', '6', '7', '8', '9',
		'a', 'b', 'c', 'd', 'e', 'f', 'g', 'h', 'i', 'j',
		'k', 'l', 'm', 'n', 'o', 'p', 'q', 'r', 's', 't',
		'u', 'v', 'w', 'x', 'y', 'z', 'd', 'a', 't', 'a'))
	f.Add(frame(2+36, byte(MsgPTYOutput), byte(CodecGob)))

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			data = data[:1<<20]
		}

		r := bytes.NewReader(data)
		// A single reader can hold several frames; a desync on one frame must
		// not turn into an unbounded read loop on the rest.
		for range 64 {
			msg, codec, err := ReadMessageWithCodec(r)
			if err != nil {
				break
			}
			if msg == nil {
				t.Fatalf("ReadMessageWithCodec returned a nil message and no error")
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
			_ = codec

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
				}
			}
		}
	})
}

// FuzzCodecDecode drives the JSON codec's decode over arbitrary bytes into each
// payload type the daemon unmarshals from a client.
func FuzzCodecDecode(f *testing.F) {
	f.Add([]byte(`{}`))
	f.Add([]byte(`null`))
	f.Add([]byte(`[]`))
	f.Add([]byte(``))
	f.Add([]byte(`{"session_id":"abc","cols":80,"rows":24}`))
	f.Add([]byte(`{"cols":-1,"rows":-1}`))
	f.Add([]byte(`{"cols":99999999999999999999,"rows":1}`))
	f.Add([]byte(strings.Repeat("[", 4096) + strings.Repeat("]", 4096)))
	f.Add([]byte(`{"a":` + strings.Repeat(`{"a":`, 2048) + `1` + strings.Repeat(`}`, 2048) + `}`))

	codec := GetCodec(CodecJSON)

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			data = data[:1<<20]
		}

		done := make(chan struct{})
		go func() {
			defer close(done)
			var m map[string]any
			_ = codec.Decode(data, &m)
			var s []any
			_ = codec.Decode(data, &s)
			var str string
			_ = codec.Decode(data, &str)
		}()

		select {
		case <-done:
		case <-time.After(30 * time.Second):
			t.Fatalf("decoding %d bytes did not terminate", len(data))
		}
	})
}
