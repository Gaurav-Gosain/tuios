package dnd

import (
	"bytes"
	"encoding/base64"
	"strconv"
	"strings"

	"tuios.dev/vt"
)

// The kitty drag and drop protocol, OSC 72.
//
// Every message of the protocol is one escape code,
//
//	OSC 72 ; metadata ; payload ST
//
// where the metadata is a colon-separated list of key=value pairs and the
// payload's meaning depends on the type key, t. The same code carries both
// directions: a program in a pane sends it to say it accepts drops (t=a) or
// offers drags (t=o), and the terminal sends it back for the pointer moving
// over the program with a drag (t=m), the drop (t=M), the data the program
// then asks for (t=r), and the events of a drag the program started (t=e).
// The specification is https://sw.kovidgoyal.net/kitty/dnd-protocol/, read
// at kitty 0.49 (October 2026); the page says the protocol is not yet
// stable.
//
// This file is the wire layer only: it reads a message out of the OSC body
// the emulator hands to Callbacks.PrivateOSC, puts a chunked message back
// together, and writes a message in the form kitty writes it. What a message
// means, and who gets it, is the host's business (internal/app).
//
// Three things about the wire that the specification does not spell out, and
// that the implementations rely on:
//
//   - A chunk after the first may carry no metadata but m: the dnd kitten
//     writes "OSC 72 ; m=1 ; ..." for every chunk past the first. kitty's
//     screen keeps the first chunk's metadata for the whole chain, and so does
//     Assembler.
//   - A data stream (t=p, t=e, t=k and the terminal's t=r) ends on a chunk
//     with m=0 and an empty payload, and only there. kitty and the kitten
//     both write m=0 on the last chunk that carries data and then the empty
//     chunk after it, and kitty's reader takes a data chunk with m=0 as data,
//     not as the end. yazi reads a t=r with a payload as "more to come"
//     whatever its m says, for the same reason. So a data chunk's m says
//     nothing, and EncodeData writes what kitty writes, which every
//     reader takes.
//   - Every program that reads the protocol stops at ST and only ST. The
//     emulator takes BEL too, as it does for every OSC, but nothing written
//     here ends in BEL.
//
// A number in the metadata is a 32-bit integer by the specification. The
// parser holds each to that range, so a value a program sends cannot
// overflow an int on any platform.

// Code is the OSC number of the drag and drop protocol.
const Code = 72

// MaxChunk is the most bytes one chunk's payload may carry, by the
// specification. Encoding splits a longer payload; parsing refuses a chunk
// past it, since every real client stays well inside and a payload of
// megabytes in one chunk is a program misbehaving.
const MaxChunk = 4096

// base64PerChunk is the raw bytes that base64 to MaxChunk, and the
// amount of data one chunk of a data stream carries. kitty uses the same.
const base64PerChunk = 3072

// Command is one message of the protocol, metadata and payload.
//
// The integer keys keep the names of the specification, because each means
// three or four things depending on the type: x is a cell column in a move,
// a 1-based MIME index in a data request, the flag that makes a t=a carry a
// machine id, and the index of the image to show in a t=P. Reading them with
// the specification open is the only way that works, so they are not renamed.
type Command struct {
	// Type is the t key: a, A, m, M, r, R, o, p, P, e, E, k or q.
	Type byte
	// More is m=1: another chunk of this message follows.
	More bool
	// ID is the i key, the multiplexer's id. A terminal that received it on a
	// t=a or t=o echoes it on everything it sends that program.
	ID int
	// Op is the o key: an operation (1 copy, 2 move, 3 either, 0 rejected), or
	// the opacity of a text drag image.
	Op int
	// X and Y are the x and y keys.
	X, Y int
	// PX and PY are the X and Y keys: pixels, an image size, or a handle.
	PX, PY int
	// Payload is the bytes after the second semicolon, as sent. Binary data
	// is base64 in it; a MIME list or a machine id is plain text.
	Payload []byte
}

// Parse reads a message from the body of an OSC 72 as the emulator passes
// it to Callbacks.PrivateOSC: "72;metadata;payload", or the metadata and
// payload without the number. It reports false for a body that is not a
// message of this protocol: a metadata part with no '=', a key with a value
// that is not an integer where one is required, a type that is not one
// character, or a payload past MaxChunk. A key the protocol does not name
// is skipped, as kitty skips it, so a later version of the protocol can add
// one without every old reader refusing the message.
//
// A body with no type key is a continuation chunk: Type is 0 and the
// assembler fills it from the first chunk of the chain.
func Parse(body []byte) (Command, bool) {
	var cmd Command
	if rest, ok := bytes.CutPrefix(body, []byte("72")); ok && (len(rest) == 0 || rest[0] == ';') {
		if len(rest) == 0 {
			return cmd, false
		}
		body = rest[1:]
	}
	meta, payload, _ := bytes.Cut(body, []byte{';'})
	if len(payload) > MaxChunk {
		return cmd, false
	}
	if len(meta) > 0 {
		for part := range bytes.SplitSeq(meta, []byte{':'}) {
			key, value, ok := bytes.Cut(part, []byte{'='})
			if !ok || len(key) != 1 {
				return cmd, false
			}
			switch key[0] {
			case 't':
				if len(value) != 1 || !typeKnown(value[0]) {
					return cmd, false
				}
				cmd.Type = value[0]
				continue
			}
			n, ok := parseInt(value)
			if !ok {
				return cmd, false
			}
			switch key[0] {
			case 'm':
				cmd.More = n == 1
			case 'i':
				cmd.ID = n
			case 'o':
				cmd.Op = n
			case 'x':
				cmd.X = n
			case 'y':
				cmd.Y = n
			case 'X':
				cmd.PX = n
			case 'Y':
				cmd.PY = n
			}
		}
	}
	// The slice is the parser's, valid for the callback only. The command
	// outlives it.
	cmd.Payload = bytes.Clone(payload)
	return cmd, true
}

// dnDType reports whether t is a type the protocol names.
func typeKnown(t byte) bool {
	return strings.IndexByte("aAmMrRopPeEkq", t) >= 0
}

// dnDInt reads a signed decimal that fits in 32 bits, as the specification
// requires of every integer. Anything else, an empty value included, is not
// a number.
func parseInt(b []byte) (int, bool) {
	if len(b) == 0 || len(b) > 11 {
		return 0, false
	}
	n, err := strconv.ParseInt(string(b), 10, 32)
	if err != nil {
		return 0, false
	}
	return int(n), true
}

// Assembler puts a chunked message back together, one per direction of
// one pane. A chunk after the first keeps only its m, so the assembler keeps
// the first chunk's metadata and hands every chunk out with it filled in, as
// kitty's screen does.
//
// It joins the payloads of a list message (t=a, t=o and t=m carry a MIME
// list) and returns the whole list once, on the last chunk. A data stream
// (t=p, t=e, t=k, t=r) is handed out chunk by chunk, each with the metadata
// of the first, so a reader can stream a file without the assembler holding
// it; the reader ends the stream on the chunk with More false and no
// payload (see the file comment). The two differ because a list is small
// and read whole, and a stream is bounded only by the reader's own limits.
type Assembler struct {
	active bool
	first  Command
	list   []byte
}

// MaxListBytes bounds a MIME list or machine id joined across chunks.
// kitty's own cap is of the same order. A chain past it is dropped whole.
const MaxListBytes = 64 << 10

// Add takes one chunk and reports the message to act on, or false when the
// chunk completes nothing yet or belongs to a chain that was dropped.
func (a *Assembler) Add(cmd Command) (Command, bool) {
	if cmd.Type == 'q' {
		// A query may come in the middle of a chain, by the specification,
		// and is no part of it.
		return cmd, true
	}
	if !a.active {
		if cmd.Type == 0 {
			// A continuation with no chain to continue. Nothing to do with it.
			return Command{}, false
		}
		if !cmd.More {
			return cmd, true
		}
		a.active = true
		a.first = cmd
		a.first.Payload = nil
		a.list = a.list[:0]
		if streamType(cmd.Type) {
			return cmd, true
		}
		return a.appendList(cmd)
	}
	// In a chain: the first chunk's metadata, this chunk's m and payload. A
	// chunk that names a type is still part of the chain, as it is in
	// kitty's screen: the specification lets a chunk repeat the metadata.
	out := a.first
	out.More = cmd.More
	out.Payload = cmd.Payload
	if !cmd.More {
		a.active = false
	}
	if streamType(out.Type) {
		return out, true
	}
	return a.appendList(out)
}

// appendList joins a list chunk and returns the list once it is whole.
func (a *Assembler) appendList(cmd Command) (Command, bool) {
	if len(a.list)+len(cmd.Payload) > MaxListBytes {
		a.active = false
		a.list = nil
		return Command{}, false
	}
	a.list = append(a.list, cmd.Payload...)
	if cmd.More {
		return Command{}, false
	}
	out := a.first
	out.More = false
	out.Payload = bytes.Clone(a.list)
	a.list = a.list[:0]
	return out, true
}

// Reset forgets a chain in progress, for a pane whose program ended.
func (a *Assembler) Reset() {
	a.active = false
	a.list = nil
}

// dnDStreamType reports whether messages of type t carry a data stream, which
// is read chunk by chunk, rather than a list, which is read whole.
func streamType(t byte) bool {
	switch t {
	case 'p', 'e', 'k', 'r':
		return true
	}
	return false
}

// Encode writes the message as one escape code, in the form kitty writes
// it: the type first, then each integer key that is not zero, then m when
// set, then the payload. A payload longer than MaxChunk is split into
// chunks that each repeat the metadata, with m=1 on all but the last. The
// payload is written as given; see EncodeData for binary data.
//
// The keys a message's meaning rests on are written at zero too: x and y of
// a gesture, a move, a drop, an event and a data message, and o of a move, a
// drop and an operation change. kitty writes them so, and yazi reads a
// message without them as malformed and drops it: a drag that ended with
// t=e:x=4 and no y=0 left yazi believing it was still dragging, refusing
// every drop from then on. Every other key at zero is left out, as the
// kitten leaves it out.
func (cmd Command) Encode() []byte {
	if len(cmd.Payload) <= MaxChunk {
		return cmd.appendChunk(nil, cmd.Payload, cmd.More)
	}
	var out []byte
	for rest := cmd.Payload; len(rest) > 0; {
		n := min(len(rest), MaxChunk)
		out = cmd.appendChunk(out, rest[:n], len(rest) > n || cmd.More)
		rest = rest[n:]
	}
	return out
}

// appendChunk writes one escape code with cmd's metadata, more as its m and
// payload as its payload.
func (cmd Command) appendChunk(out, payload []byte, more bool) []byte {
	out = append(out, "\x1b]72;t="...)
	out = append(out, cmd.Type)
	if cmd.ID != 0 {
		out = strconv.AppendInt(append(out, ":i="...), int64(cmd.ID), 10)
	}
	leave := cmd.Type == 'm' && cmd.X == -1 && cmd.Y == -1
	position := strings.IndexByte("mMoerR", cmd.Type) >= 0
	pixels := !leave && (cmd.Type == 'm' || cmd.Type == 'M' || cmd.Type == 'o' && cmd.X != 1 && cmd.X != 2)
	if cmd.Op != 0 || (pixels && cmd.Type != 'o') || (cmd.Type == 'e' && cmd.X == 2) {
		out = strconv.AppendInt(append(out, ":o="...), int64(cmd.Op), 10)
	}
	if cmd.X != 0 || position {
		out = strconv.AppendInt(append(out, ":x="...), int64(cmd.X), 10)
	}
	if cmd.Y != 0 || position {
		out = strconv.AppendInt(append(out, ":y="...), int64(cmd.Y), 10)
	}
	if cmd.PX != 0 || pixels {
		out = strconv.AppendInt(append(out, ":X="...), int64(cmd.PX), 10)
	}
	if cmd.PY != 0 || pixels {
		out = strconv.AppendInt(append(out, ":Y="...), int64(cmd.PY), 10)
	}
	if more {
		out = append(out, ":m=1"...)
	} else if len(payload) > 0 {
		// A data chunk says it is the last one, as kitty's do, so a reader
		// that ends a stream on m=0 ends it here.
		out = append(out, ":m=0"...)
	}
	if len(payload) > 0 {
		out = append(out, ';')
		out = append(out, payload...)
	}
	return append(out, "\x1b\\"...)
}

// EncodeData writes data as a stream of messages with hdr's type and
// keys, the way kitty writes a data response: base64, in chunks of
// base64PerChunk raw bytes, m=1 on every chunk but the last data chunk,
// and after them an empty message that closes the stream. Empty data is the
// closing message alone.
func EncodeData(hdr Command, data []byte) []byte {
	var out []byte
	for rest := data; len(rest) > 0; {
		n := min(len(rest), base64PerChunk)
		enc := make([]byte, base64.StdEncoding.EncodedLen(n))
		base64.StdEncoding.Encode(enc, rest[:n])
		rest = rest[n:]
		out = hdr.appendChunk(out, enc, len(rest) > 0)
	}
	return hdr.appendChunk(out, nil, false)
}

// ErrorPayload is the payload of an error message (t=R or t=E): a POSIX
// error name and an optional description, which may hold only text that is
// safe to print. The description is cleaned here so a caller can put a
// program's own words in it.
func ErrorPayload(errno, description string) []byte {
	if description == "" {
		return []byte(errno)
	}
	return []byte(errno + ":" + vt.StripControls(description))
}

// DecodePayload decodes a base64 payload. Padding is optional in the
// protocol, and the kitten leaves it off, so both forms are read.
func DecodePayload(payload []byte) ([]byte, error) {
	s := strings.TrimRight(string(payload), "=")
	return base64.RawStdEncoding.DecodeString(s)
}

// MimeList reads a space-separated MIME list. An empty payload is an
// empty list. Each entry is held to printable ASCII, since a MIME type is
// ASCII and the list goes back out to another program in a message of its
// own.
func MimeList(payload []byte) []string {
	fields := strings.Fields(string(payload))
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		clean := true
		for i := 0; i < len(f); i++ {
			if f[i] <= 0x20 || f[i] >= 0x7f {
				clean = false
				break
			}
		}
		if clean {
			out = append(out, f)
		}
	}
	return out
}
