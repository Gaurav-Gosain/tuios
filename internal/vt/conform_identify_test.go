package vt_test

// Conformance for the queries a guest uses to identify the terminal and read
// its state back: XTVERSION, DA3, XTGETTCAP, and DECRQSS for the pen and the
// cursor shape.
//
// Reply formats are xterm's ctlseqs: XTVERSION is DCS > | text ST, DA3 is
// DCS ! | unit-id ST, XTGETTCAP is DCS 1 + r name=value ST per name with
// DCS 0 + r ST for a name the terminal does not have, and DECRQSS is
// DCS 1 $ r value ST.

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/Gaurav-Gosain/tuios/internal/vt"
)

// replyChecked runs input into a fresh emulator and returns what it wrote back
// to the guest, failing the test if any part of the input was logged as an
// unrecognised sequence. A query that is answered and also logged as
// unhandled is answered by accident.
func replyChecked(t *testing.T, in string) string {
	t.Helper()
	emu := vt.NewEmulator(80, 24)
	log := &unhandledLog{}
	emu.SetLogger(log)
	if _, err := emu.WriteString(in); err != nil {
		t.Fatalf("write %q: %v", in, err)
	}
	if len(log.lines) > 0 {
		t.Errorf("input %q was logged as unrecognised: %s", in, strings.Join(log.lines, "; "))
	}
	return drainReply(emu)
}

// drainReply returns everything the emulator has written back so far. Replies
// are written during the Write that provokes them, so they are all buffered by
// now; a short sentinel query marks the end, so the read never blocks.
func drainReply(emu *vt.Emulator) string {
	const sentinel = "\x1b[5n"
	_, _ = emu.WriteString(sentinel)
	var b strings.Builder
	buf := make([]byte, 4096)
	for !strings.HasSuffix(b.String(), "\x1b[0n") {
		n, err := emu.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return strings.TrimSuffix(b.String(), "\x1b[0n")
}

func TestConform_XTVERSION(t *testing.T) {
	for _, in := range []string{"\x1b[>q", "\x1b[>0q"} {
		if got, want := replyChecked(t, in), "\x1bP>|tuios\x1b\\"; got != want {
			t.Errorf("%q replied %q, want %q", in, got, want)
		}
	}

	// A release build names its version after the program name, as tmux and
	// WezTerm do.
	vt.SetBuildVersion("1.2.3")
	t.Cleanup(func() { vt.SetBuildVersion("") })
	if got, want := replyChecked(t, "\x1b[>q"), "\x1bP>|tuios 1.2.3\x1b\\"; got != want {
		t.Errorf("with a build version, replied %q, want %q", got, want)
	}
	if got, want := vt.XTVersionName(), "tuios 1.2.3"; got != want {
		t.Errorf("XTVersionName() = %q, want %q", got, want)
	}
}

func TestConform_DA3(t *testing.T) {
	for _, in := range []string{"\x1b[=c", "\x1b[=0c"} {
		if got, want := replyChecked(t, in), "\x1bP!|00000000\x1b\\"; got != want {
			t.Errorf("%q replied %q, want %q", in, got, want)
		}
	}
}

// hexName spells a capability name the way XTGETTCAP carries it.
func hexName(s string) string { return hex.EncodeToString([]byte(s)) }

func TestConform_XTGETTCAP(t *testing.T) {
	ok := func(name, value string) string {
		return "\x1bP1+r" + hexName(name) + "=" + strings.ToUpper(hex.EncodeToString([]byte(value))) + "\x1b\\"
	}
	boolean := func(name string) string { return "\x1bP1+r" + hexName(name) + "\x1b\\" }
	const refused = "\x1bP0+r\x1b\\"
	query := func(names ...string) string {
		for i, n := range names {
			names[i] = hexName(n)
		}
		return "\x1bP+q" + strings.Join(names, ";") + "\x1b\\"
	}

	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"TN", query("TN"), ok("TN", "xterm-256color")},
		{"colors", query("colors"), ok("colors", "256")},
		{"the termcap name for colors", query("Co"), ok("Co", "256")},
		{"RGB is a boolean", query("RGB"), boolean("RGB")},
		{"Tc is a boolean", query("Tc"), boolean("Tc")},
		{"Su is a boolean", query("Su"), boolean("Su")},
		{"Smulx", query("Smulx"), ok("Smulx", "\x1b[4:%p1%dm")},
		{"Setulc", query("Setulc"), ok("Setulc", "\x1b[58:2:%p1%{65536}%/%d:%p1%{256}%/%{255}%&%d:%p1%{255}%&%d%;m")},
		{"Ms", query("Ms"), ok("Ms", "\x1b]52;%p1%s;%p2%s\x07")},
		{"Ss", query("Ss"), ok("Ss", "\x1b[%p1%d q")},
		{"Se", query("Se"), ok("Se", "\x1b[2 q")},
		{"Sync", query("Sync"), ok("Sync", "\x1b[?2026%?%p1%{1}%-%tl%eh%;")},

		// One reply per name, in the order asked, a refusal included.
		{"two names", query("TN", "RGB"), ok("TN", "xterm-256color") + boolean("RGB")},
		{"an unknown name between two known ones", query("TN", "nope", "RGB"),
			ok("TN", "xterm-256color") + refused + boolean("RGB")},

		{"an unknown name is refused", query("nope"), refused},
		{"a name that is not hex is refused", "\x1bP+qzz\x1b\\", refused},
		{"an empty request is refused", "\x1bP+q\x1b\\", refused},

		// The name comes back as the guest spelled it, case included.
		{"uppercase hex", "\x1bP+q544E\x1b\\", "\x1bP1+r544E=787465726D2D323536636F6C6F72\x1b\\"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := replyChecked(t, tc.in); got != tc.want {
				t.Errorf("reply = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestConform_DECRQSS_SGR pins the pen as DECRQSS reports it. xterm starts the
// value with 0, so sending it back clears whatever it does not name.
func TestConform_DECRQSS_SGR(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"a reset pen", "", "0m"},
		{"after SGR 0", "\x1b[1;31m\x1b[0m", "0m"},
		{"bold red", "\x1b[1;31m", "0;1;31m"},
		{"every attribute", "\x1b[1;2;3;4;5;6;7;8;9m", "0;1;2;3;4;5;6;7;8;9m"},
		{"attributes turned off again", "\x1b[1;3;7m\x1b[22;27m", "0;3m"},
		{"bright colours", "\x1b[91;102m", "0;91;102m"},
		{"an indexed colour keeps its index", "\x1b[38;5;1;48;5;200m", "0;38;5;1;48;5;200m"},
		{"direct colour", "\x1b[38;2;1;2;3;48:2:4:5:6m", "0;38;2;1;2;3;48;2;4;5;6m"},
		{"a double underline from SGR 21", "\x1b[21m", "0;4:2m"},
		{"a curly underline", "\x1b[4:3m", "0;4:3m"},
		{"an underline colour", "\x1b[4;58;5;9m", "0;4;58;5;9m"},
		{"a direct underline colour", "\x1b[58;2;10;20;30m", "0;58;2;10;20;30m"},
		{"default colours are not named", "\x1b[31;41m\x1b[39;49m", "0m"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, want := replyChecked(t, tc.in+"\x1bP$qm\x1b\\"), "\x1bP1$r"+tc.want+"\x1b\\"; got != want {
				t.Errorf("reply = %q, want %q", got, want)
			}
		})
	}
}

// TestConform_DECRQSS_SGRRoundTrip checks the property the reply exists for:
// a guest that saves the pen with DECRQSS and later sends the value back gets
// the same pen.
func TestConform_DECRQSS_SGRRoundTrip(t *testing.T) {
	for _, sgr := range []string{
		"1;31", "2;3;4:5;44", "7;9;38;5;123;48;2;9;8;7", "21;58:2:1:2:3", "5;6;8;97;107",
		"38:2::10:20:30",
	} {
		t.Run(sgr, func(t *testing.T) {
			first := vt.NewEmulator(10, 2)
			_, _ = first.WriteString("\x1b[" + sgr + "mx\x1bP$qm\x1b\\")
			got := drainReply(first)
			value, ok := strings.CutPrefix(got, "\x1bP1$r")
			value, ok2 := strings.CutSuffix(value, "\x1b\\")
			if !ok || !ok2 {
				t.Fatalf("reply %q is not a DECRQSS answer", got)
			}
			second := vt.NewEmulator(10, 2)
			_, _ = second.WriteString("\x1b[" + value + "x")
			a, b := first.CellAt(0, 0).Style, second.CellAt(0, 0).Style
			if !a.Equal(&b) {
				t.Errorf("SGR %s reported as %q, which reads back as a different pen: %+v, want %+v", sgr, value, b, a)
			}
		})
	}
}

func TestConform_DECRQSS_DECSCUSR(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		// A pane starts with a steady block, which DECSCUSR spells 2.
		{"the default shape", "", "2 q"},
		{"a blinking block", "\x1b[1 q", "1 q"},
		{"DECSCUSR 0 is a blinking block", "\x1b[0 q", "1 q"},
		{"a blinking underline", "\x1b[3 q", "3 q"},
		{"a steady underline", "\x1b[4 q", "4 q"},
		{"a blinking bar", "\x1b[5 q", "5 q"},
		{"a steady bar", "\x1b[6 q", "6 q"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, want := replyChecked(t, tc.in+"\x1bP$q q\x1b\\"), "\x1bP1$r"+tc.want+"\x1b\\"; got != want {
				t.Errorf("reply = %q, want %q", got, want)
			}
		})
	}

	// DECSCA is refused: nothing here protects a cell, so there is no
	// attribute to report.
	if got, want := replyChecked(t, "\x1bP$q\"q\x1b\\"), "\x1bP0$r\x1b\\"; got != want {
		t.Errorf("DECSCA request replied %q, want the refusal %q", got, want)
	}
}
