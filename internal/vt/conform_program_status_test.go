package vt_test

// Conformance for OSC 7501, the Program Status Protocol (revision 0.2), on
// the backend this build selects. The grammar and the discard rules are
// tested in internal/progstatus; this is the emulator's part: the sequence is
// recognised with either terminator and across writes, the query gets the one
// fixed reply in the query's own form, a report gets no reply at all, and a
// full reset (RIS) reaches the owner while a soft reset (DECSTR) does not.

import (
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/progstatus"
	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// programStatusEvents writes each chunk to a fresh terminal of this build's
// backend and returns the ProgramStatus events and what it wrote back.
func programStatusEvents(t *testing.T, chunks ...string) ([]progstatus.Event, string) {
	t.Helper()
	term := vt.New(80, 24)
	t.Cleanup(func() { _ = term.Close() })
	var got []progstatus.Event
	term.SetCallbacks(vt.Callbacks{ProgramStatus: func(ev progstatus.Event) { got = append(got, ev) }})
	for _, c := range chunks {
		if _, err := term.Write([]byte(c)); err != nil {
			t.Fatalf("write %q: %v", c, err)
		}
	}
	// A device status report marks the end of whatever the input provoked.
	if _, err := term.Write([]byte("\x1b[5n")); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	buf := make([]byte, 4096)
	for !strings.HasSuffix(b.String(), "\x1b[0n") {
		n, err := term.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return got, strings.TrimSuffix(b.String(), "\x1b[0n")
}

func TestConform_ProgramStatusQueryReply(t *testing.T) {
	for _, term := range []string{"\x1b\\", "\x07"} {
		_, reply := programStatusEvents(t, "\x1b]7501;?"+term)
		if want := "\x1b]7501;?" + term; reply != want {
			t.Errorf("query ended with %q replied %q, want %q", term, reply, want)
		}
	}
	// Only the exact query is answered: a report is never echoed, so no id,
	// title or message can be read back.
	for _, in := range []string{
		"\x1b]7501;state=working:msg=aGk=\x1b\\",
		"\x1b]7501;??\x1b\\",
		"\x1b]7501;?:state=idle\x1b\\",
		"\x1b]7501\x1b\\",
	} {
		if _, reply := programStatusEvents(t, in); reply != "" {
			t.Errorf("%q replied %q, want nothing", in, reply)
		}
	}
}

func TestConform_ProgramStatusPureRecognisesTheSequence(t *testing.T) {
	// replyChecked fails when the pure emulator logs the input as a sequence
	// it did not recognise.
	if got := replyChecked(t, "\x1b]7501;state=done:app=cargo\x1b\\\x1b]7501;?\x07"); got != "\x1b]7501;?\x07" {
		t.Errorf("reply = %q", got)
	}
}

func TestConform_ProgramStatusReports(t *testing.T) {
	for _, tc := range []struct {
		name   string
		chunks []string
		want   []progstatus.Event
	}{
		{
			name:   "ended with ST",
			chunks: []string{"\x1b]7501;state=working:app=cargo:progress=40\x1b\\"},
			want:   []progstatus.Event{{Report: progstatus.Report{State: progstatus.Working, App: "cargo", Progress: 40}}},
		},
		{
			name:   "ended with BEL",
			chunks: []string{"\x1b]7501;state=blocked:kind=permission:msg=QXBwbHk/\x07"},
			want:   []progstatus.Event{{Report: progstatus.Report{State: progstatus.Blocked, Kind: progstatus.Permission, Msg: "Apply?", Progress: -1}}},
		},
		{
			name:   "split across writes",
			chunks: []string{"\x1b]75", "01;sta", "te=done:id=bu", "ild/test\x1b", "\\"},
			want:   []progstatus.Event{{Report: progstatus.Report{State: progstatus.Done, ID: "build/test", Progress: -1}}},
		},
		{
			name:   "one byte at a time",
			chunks: strings.Split("\x1b]7501;state=error\x07", ""),
			want:   []progstatus.Event{{Report: progstatus.Report{State: progstatus.Error, Progress: -1}}},
		},
		{
			name:   "a discarded report is not handed on",
			chunks: []string{"\x1b]7501;state=working:msg=a\x1b\\", "\x1b]7501;state=sleeping\x1b\\", "\x1b]7501;state=idle:id=a//b\x1b\\"},
			want:   nil,
		},
		{
			name:   "a report over 4096 bytes is discarded",
			chunks: []string{"\x1b]7501;state=idle:x=" + strings.Repeat("a", 4096) + "\x1b\\"},
			want:   nil,
		},
		{
			name:   "RIS removes every record",
			chunks: []string{"\x1bc"},
			want:   []progstatus.Event{{Reset: true}},
		},
		{
			name:   "DECSTR does not",
			chunks: []string{"\x1b[!p"},
			want:   nil,
		},
		{
			name:   "the screen does not matter",
			chunks: []string{"\x1b[?1049h\x1b]7501;state=idle\x1b\\\x1b[?1049l"},
			want:   []progstatus.Event{{Report: progstatus.Report{State: progstatus.Idle, Progress: -1}}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, reply := programStatusEvents(t, tc.chunks...)
			if reply != "" {
				t.Errorf("reply = %q, want nothing", reply)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("events = %+v, want %+v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("event %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}
