package vt_test

import (
	"bytes"
	"encoding/base64"
	"strconv"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// Fuzzing the kitty graphics command parser and its payload decoder against
// the encodings real senders produce.
//
// The ways these could fail, written down before the targets:
//
//  1. DecodeKittyPayload rejects what a sender produces: padded base64 of any
//     bytes (chafa, timg, mpv), unpadded base64 (kitten icat), or padded
//     chunks joined into one payload.
//  2. It accepts one of those and returns bytes other than the ones encoded.
//  3. It disagrees with the standard library on a string the library decodes
//     strictly, padded or not.
//  4. It panics on padding in odd places, a length one past a group, or the
//     line breaks the library skips.
//  5. ParseKittyCommand loses the payload text (RawPayload must be exactly
//     the bytes after the first ';', which the passthrough forwards as is),
//     or decodes a direct payload to something other than its bytes.
//  6. A real transmission chunk is taken for a terminal's reply echoed back
//     and dropped. A short chunk of dark pixels encodes to an E and capital
//     letters, the shape of "EINVAL"; the last chunk of a chunked image is
//     often that short, and dropping it left the image without its m=0.
//  7. A real reply ("i=3;OK", "i=3;ENOENT:msg") is not taken for one, and a
//     guest that echoes input starts a loop of replies.

func FuzzKittyPayloadDecode(f *testing.F) {
	f.Add([]byte{}, uint8(0))
	f.Add([]byte("a"), uint8(1))
	f.Add([]byte("ab"), uint8(1))
	f.Add([]byte("abc"), uint8(2))
	f.Add([]byte{0x10, 0, 0, 0, 0, 0}, uint8(3))
	f.Add([]byte("QUJD=="), uint8(0))
	f.Add([]byte("=A=="), uint8(0))
	f.Add([]byte("AAAA\nAAAA"), uint8(0))
	f.Add([]byte("QQ==QQ=="), uint8(0))
	f.Add([]byte("Q"), uint8(0))

	f.Fuzz(func(t *testing.T, data []byte, cut uint8) {
		if len(data) > 1<<14 {
			return
		}
		decode := func(what, s string, want []byte) {
			t.Helper()
			got, err := vt.DecodeKittyPayload([]byte(s))
			if err != nil {
				t.Fatalf("%s %q was refused: %v", what, s, err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("%s %q decoded to %x, want %x", what, s, got, want)
			}
		}
		decode("padded base64", base64.StdEncoding.EncodeToString(data), data)
		decode("unpadded base64", base64.RawStdEncoding.EncodeToString(data), data)
		k := int(cut) % (len(data) + 1)
		decode("two padded chunks joined",
			base64.StdEncoding.EncodeToString(data[:k])+base64.StdEncoding.EncodeToString(data[k:]), data)

		// The same bytes read as text: wherever the library decodes it, the
		// decoder must agree.
		s := string(data)
		got, err := vt.DecodeKittyPayload(data)
		for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding} {
			want, werr := enc.DecodeString(s)
			if werr != nil {
				continue
			}
			if err != nil {
				t.Fatalf("%q decodes with the standard library and was refused: %v", s, err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("%q decoded to %x, the standard library says %x", s, got, want)
			}
		}
	})
}

// kittyControlKeys are the keys a guest's control data may carry.
var kittyControlKeys = []string{"a", "q", "i", "I", "p", "f", "t", "o", "s", "v", "S", "O", "m", "d", "x", "y", "w", "h", "X", "Y", "c", "r", "z", "C", "U"}

func FuzzKittyCommand(f *testing.F) {
	f.Add([]byte{0x10, 0, 0, 0, 0, 0}, uint8(12), uint32(1), true)
	f.Add([]byte("hello"), uint8(0), uint32(7), false)
	f.Add([]byte{}, uint8(5), uint32(0), false)
	f.Add([]byte{0xff, 0xfe, 0xfd}, uint8(200), uint32(4294967295), true)

	f.Fuzz(func(t *testing.T, payload []byte, keys uint8, id uint32, padded bool) {
		if len(payload) > 1<<14 {
			return
		}
		enc := base64.RawStdEncoding.EncodeToString(payload)
		if padded {
			enc = base64.StdEncoding.EncodeToString(payload)
		}

		// A continuation chunk: m= and nothing else but the id. Every chunk
		// after the first looks like this.
		for _, more := range []string{"0", "1"} {
			ctrl := "i=" + strconv.FormatUint(uint64(id), 10) + ",m=" + more
			cmd, err := vt.ParseKittyCommand([]byte(ctrl + ";" + enc))
			if err != nil || cmd == nil {
				t.Fatalf("%q: %v", ctrl, err)
			}
			if cmd.RawPayload != enc {
				t.Fatalf("RawPayload %q, want %q", cmd.RawPayload, enc)
			}
			if cmd.ImageID != id {
				t.Fatalf("ImageID %d, want %d", cmd.ImageID, id)
			}
			if cmd.More != (more == "1") {
				t.Fatalf("More %v for m=%s", cmd.More, more)
			}
			if len(payload) > 0 && (cmd.PayloadErr != nil || !bytes.Equal(cmd.Data, payload)) {
				t.Fatalf("a direct chunk decoded to %x (%v), want %x", cmd.Data, cmd.PayloadErr, payload)
			}
			if vt.IsKittyEchoedResponse(cmd) {
				t.Fatalf("a transmission chunk %q was taken for an echoed reply", ctrl+";"+enc)
			}
		}

		// A first chunk with a few more keys, picked by the bits of keys.
		var parts []string
		for i, k := range kittyControlKeys {
			if keys&(1<<(i%8)) != 0 && i/8 == int(keys)%3 {
				parts = append(parts, k+"=1")
			}
		}
		parts = append(parts, "a=T", "f=24")
		cmd, _ := vt.ParseKittyCommand([]byte(strings.Join(parts, ",") + ";" + enc))
		if cmd == nil || vt.IsKittyEchoedResponse(cmd) {
			t.Fatalf("a first chunk %q was taken for an echoed reply", strings.Join(parts, ","))
		}

		// Replies, with the keys a terminal puts on them.
		for _, reply := range []string{"OK", "ENOENT", "EINVAL:bad " + strconv.Itoa(int(keys))} {
			for _, ctrl := range []string{"", "i=" + strconv.FormatUint(uint64(id), 10), "i=1,p=2", "I=9"} {
				cmd, _ := vt.ParseKittyCommand([]byte(ctrl + ";" + reply))
				if cmd == nil || !vt.IsKittyEchoedResponse(cmd) {
					t.Fatalf("the reply %q was not taken for one", ctrl+";"+reply)
				}
			}
		}
	})
}
