package vt

import (
	"bytes"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestAnIdleReleaseGivesBackTheSequenceBuffer(t *testing.T) {
	// payload is an APC body no built-in handler takes: X, then x bytes.
	payload := func(n int) []byte {
		p := bytes.Repeat([]byte{'x'}, n)
		p[0] = 'X'
		return p
	}
	apc := func(p []byte) []byte {
		return append(append([]byte("\x1b_"), p...), "\x1b\\"...)
	}
	// recording returns an emulator whose APC handler keeps a copy of the
	// last payload it is handed: the parser hands it its own buffer.
	recording := func() (*Emulator, *[]byte) {
		e := NewEmulator(80, 24)
		got := new([]byte)
		e.RegisterApcHandler(func(data []byte) bool {
			*got = bytes.Clone(data)
			return true
		})
		return e, got
	}
	write := func(t *testing.T, e *Emulator, b []byte) {
		t.Helper()
		if _, err := e.Write(b); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	given := func(t *testing.T, e *Emulator) {
		t.Helper()
		if n, c := len(e.parser.data), cap(e.parser.data); n != seqParserInitialData || c != seqParserInitialData {
			t.Errorf("after the release the buffer has len %d cap %d, want %d", n, c, seqParserInitialData)
		}
	}
	// stillWhole writes a further 1 MiB + 1 KiB payload and checks it arrives
	// whole after the release.
	stillWhole := func(t *testing.T, e *Emulator, got *[]byte) {
		t.Helper()
		want := payload(1<<20 + 1<<10)
		*got = nil
		write(t, e, apc(want))
		if !bytes.Equal(*got, want) {
			t.Errorf("a further payload of %d bytes arrived as %d bytes", len(want), len(*got))
		}
	}

	for _, tc := range []struct {
		name  string
		size  int
		grown int
	}{
		{"one MiB and one KiB", 1<<20 + 1<<10, 2 << 20},
		{"three MiB", 3 << 20, 4 << 20},
	} {
		t.Run("after an APC payload of "+tc.name, func(t *testing.T) {
			e, got := recording()
			write(t, e, apc(payload(tc.size)))
			if n := len(e.parser.data); n != tc.grown {
				t.Fatalf("before the release the buffer is %d bytes, want %d", n, tc.grown)
			}
			e.ReleaseReflowScratch()
			given(t, e)
			if e.parser.dataLen != 0 {
				t.Errorf("after the release dataLen is %d, want 0", e.parser.dataLen)
			}
			stillWhole(t, e, got)
		})
	}

	t.Run("after a payload one byte past the initial buffer", func(t *testing.T) {
		e, got := recording()
		write(t, e, apc(payload(seqParserInitialData+1)))
		if n := len(e.parser.data); n != 8<<10 {
			t.Fatalf("before the release the buffer is %d bytes, want %d", n, 8<<10)
		}
		e.ReleaseReflowScratch()
		given(t, e)
		stillWhole(t, e, got)
	})

	for _, tc := range []struct {
		name  string
		input string
	}{
		{"after only small OSCs", "\x1b]2;~/src\x07\x1b]7;file://host/home/user/src\x1b\\"},
		{"a fresh emulator", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, got := recording()
			write(t, e, []byte(tc.input))
			old := e.parser.data
			e.ReleaseReflowScratch()
			if len(e.parser.data) != len(old) || &e.parser.data[0] != &old[0] {
				t.Errorf("the release replaced a buffer that never grew (len %d, was %d)", len(e.parser.data), len(old))
			}
			stillWhole(t, e, got)
		})
	}
}

func TestAReleaseMidSequenceKeepsThePayload(t *testing.T) {
	body := bytes.Repeat([]byte{'x'}, 64<<10)
	body[0] = 'X'

	// Each case is one string sequence kind: its introducer, the payload its
	// handler is handed, and a register that records a copy of it. An OSC
	// reaches the buffer byte by byte through put, an APC or a DCS through
	// putRun.
	for _, tc := range []struct {
		name     string
		intro    string
		want     []byte
		register func(e *Emulator, got *[]byte)
	}{
		{"APC", "\x1b_", body, func(e *Emulator, got *[]byte) {
			e.RegisterApcHandler(func(data []byte) bool {
				*got = bytes.Clone(data)
				return true
			})
		}},
		{"DCS", "\x1bPq", body, func(e *Emulator, got *[]byte) {
			e.RegisterDcsHandler(int('q'), func(_ ansi.Params, data []byte) bool {
				*got = bytes.Clone(data)
				return true
			})
		}},
		{"OSC", "\x1b]", append([]byte("5713;"), body...), func(e *Emulator, got *[]byte) {
			e.RegisterOscHandler(5713, func(data []byte) bool {
				*got = bytes.Clone(data)
				return true
			})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := NewEmulator(80, 24)
			var got []byte
			tc.register(e, &got)
			half := len(tc.want) / 2

			if _, err := e.Write(append([]byte(tc.intro), tc.want[:half]...)); err != nil {
				t.Fatalf("Write: %v", err)
			}
			if n := len(e.parser.data); n <= seqParserInitialData {
				t.Fatalf("after half the payload the buffer is %d bytes, want more than %d", n, seqParserInitialData)
			}
			e.ReleaseReflowScratch()
			if _, err := e.Write(append(append([]byte(nil), tc.want[half:]...), "\x1b\\"...)); err != nil {
				t.Fatalf("Write: %v", err)
			}
			if !bytes.Equal(got, tc.want) {
				t.Fatalf("a payload of %d bytes split by a release arrived as %d bytes", len(tc.want), len(got))
			}

			e.ReleaseReflowScratch()
			if n, c := len(e.parser.data), cap(e.parser.data); n != seqParserInitialData || c != seqParserInitialData {
				t.Errorf("a release at ground left the buffer at len %d cap %d, want %d", n, c, seqParserInitialData)
			}
			if e.parser.dataLen != 0 {
				t.Errorf("a release at ground left dataLen %d, want 0", e.parser.dataLen)
			}
		})
	}
}
