package vt_test

import (
	"bytes"
	"encoding/base64"
	"strconv"
	"strings"
	"testing"

	"tuios.dev/vt"
)

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
		padded := base64.StdEncoding.EncodeToString(data)
		for _, width := range []int{76, int(cut)%13 + 1} {
			var wrapped strings.Builder
			for i := 0; i < len(padded); i += width {
				wrapped.WriteString(padded[i:min(i+width, len(padded))])
				wrapped.WriteByte('\n')
			}
			decode("wrapped padded base64", wrapped.String(), data)
		}
		k := int(cut) % (len(data) + 1)
		decode("two padded chunks joined",
			base64.StdEncoding.EncodeToString(data[:k])+base64.StdEncoding.EncodeToString(data[k:]), data)

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
