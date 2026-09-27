// Command hostterm stands in for a host terminal with colours of its own, for
// the tests that need a terminal tuitest's emulator cannot be: one on a light
// background, one whose palette is not the xterm default, and one that
// switches between light and dark while tuios runs.
//
// It has two modes.
//
// hostterm run [flags] -- argv... runs argv in a PTY of its own and sits
// between it and the real terminal (tuitest's). Everything passes through
// unchanged except the colour questions: an OSC 10, OSC 11 or OSC 4 query is
// answered here with the colours given on the command line and never reaches
// the outer terminal, so tuitest's own black answer cannot race it. A DSR 996
// (colour scheme query) is answered with the current scheme. SIGUSR1 moves
// to the next scheme (-alt-bg and -alt-fg take a comma-separated list, and
// the last one wraps round to the first), and when argv has turned on mode
// 2031 it is told with a DSR 997 notification, the way ghostty and kitty tell
// a program the system appearance changed. With -mute every colour question
// is swallowed and none is answered, which is what mosh does.
//
// hostterm query SPEC asks the terminal it runs in one colour question (SPEC
// is 10, 11 or 4;N), waits for the answer and prints it as
// HOSTTERM-SPEC=answer on one line, so a test can read what a pane program
// is told through capture-pane.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/charmbracelet/x/term"
	"github.com/creack/pty"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: hostterm run [flags] -- argv... | hostterm query SPEC")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "run":
		os.Exit(run(os.Args[2:]))
	case "query":
		if len(os.Args) != 3 {
			fmt.Fprintln(os.Stderr, "usage: hostterm query SPEC")
			os.Exit(2)
		}
		os.Exit(query(os.Args[2]))
	default:
		fmt.Fprintf(os.Stderr, "hostterm: unknown mode %q\n", os.Args[1])
		os.Exit(2)
	}
}

// scheme is one appearance of the host: its default colours and whether it
// calls itself light.
type scheme struct {
	fg, bg string
	light  bool
}

// host is the state the proxy answers from.
type host struct {
	mu         sync.Mutex
	schemes    []scheme
	current    int
	ansi       map[int]string
	subscribed bool // mode 2031 is on
	// mute answers no colour question at all. The questions are still taken
	// out, so the outer terminal cannot answer them either.
	mute bool
	log  io.Writer
}

func (h *host) now() scheme {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.schemes[h.current]
}

func (h *host) note(format string, args ...any) {
	if h.log == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	fmt.Fprintf(h.log, format+"\n", args...)
}

// xrgb spells #rrggbb the way xterm answers a colour query.
func xrgb(hex string) string {
	hex = strings.TrimPrefix(hex, "#")
	if len(hex) != 6 {
		return "rgb:0000/0000/0000"
	}
	return fmt.Sprintf("rgb:%s%s/%s%s/%s%s", hex[0:2], hex[0:2], hex[2:4], hex[2:4], hex[4:6], hex[4:6])
}

func run(args []string) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fg := fs.String("fg", "#1f1f1f", "default foreground")
	bg := fs.String("bg", "#fdf6e3", "default background")
	altFg := fs.String("alt-fg", "#e0e0e0", "foregrounds after each SIGUSR1, comma-separated")
	altBg := fs.String("alt-bg", "#1e1e2e", "backgrounds after each SIGUSR1, comma-separated")
	ansiSpec := fs.String("ansi", "", "palette slots, as N=#rrggbb,N=#rrggbb")
	logPath := fs.String("log", "", "append every answered question here")
	mute := fs.Bool("mute", false, "swallow every colour question and answer none, as mosh does")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	argv := fs.Args()
	if len(argv) == 0 {
		fmt.Fprintln(os.Stderr, "hostterm run: no command")
		return 2
	}

	h := &host{ansi: map[int]string{}, mute: *mute}
	h.schemes = append(h.schemes, scheme{fg: *fg, bg: *bg, light: isLight(*bg)})
	fgs, bgs := strings.Split(*altFg, ","), strings.Split(*altBg, ",")
	if len(fgs) != len(bgs) {
		fmt.Fprintln(os.Stderr, "hostterm run: -alt-fg and -alt-bg need as many entries")
		return 2
	}
	for i := range bgs {
		h.schemes = append(h.schemes, scheme{fg: fgs[i], bg: bgs[i], light: isLight(bgs[i])})
	}
	for _, part := range strings.Split(*ansiSpec, ",") {
		if part == "" {
			continue
		}
		n, hex, ok := strings.Cut(part, "=")
		idx, err := strconv.Atoi(n)
		if !ok || err != nil || idx < 0 || idx > 255 {
			fmt.Fprintf(os.Stderr, "hostterm run: bad -ansi entry %q\n", part)
			return 2
		}
		h.ansi[idx] = hex
	}
	if *logPath != "" {
		f, err := os.OpenFile(*logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "hostterm run: %v\n", err)
			return 1
		}
		defer f.Close()
		h.log = f
	}

	cmd := exec.Command(argv[0], argv[1:]...)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hostterm run: %v\n", err)
		return 1
	}
	defer ptmx.Close()
	_ = pty.InheritSize(os.Stdin, ptmx)

	if old, err := term.MakeRaw(os.Stdin.Fd()); err == nil {
		defer func() { _ = term.Restore(os.Stdin.Fd(), old) }()
	}

	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGWINCH, syscall.SIGUSR1)
	go func() {
		for sig := range sigs {
			switch sig {
			case syscall.SIGWINCH:
				_ = pty.InheritSize(os.Stdin, ptmx)
			case syscall.SIGUSR1:
				h.mu.Lock()
				h.current = (h.current + 1) % len(h.schemes)
				now, sub := h.schemes[h.current], h.subscribed
				h.mu.Unlock()
				light := now.light
				h.note("switch to %s light=%t subscribed=%t", now.bg, light, sub)
				if sub {
					_, _ = ptmx.Write([]byte(dsr997(light)))
				}
			}
		}
	}()

	go func() { _, _ = io.Copy(ptmx, os.Stdin) }()

	f := &filter{h: h, answer: func(b string) { _, _ = ptmx.Write([]byte(b)) }}
	buf := make([]byte, 64*1024)
	for {
		n, err := ptmx.Read(buf)
		if n > 0 {
			if out := f.feed(buf[:n]); len(out) > 0 {
				if _, werr := os.Stdout.Write(out); werr != nil {
					break
				}
			}
		}
		if err != nil {
			break
		}
	}
	if err := cmd.Wait(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode()
		}
		return 1
	}
	return 0
}

func dsr997(light bool) string {
	if light {
		return "\x1b[?997;2n"
	}
	return "\x1b[?997;1n"
}

// isLight is the plain test the proxy uses to say which scheme it is in.
func isLight(hex string) bool {
	hex = strings.TrimPrefix(hex, "#")
	v, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return false
	}
	r, g, b := float64(v>>16&0xff), float64(v>>8&0xff), float64(v&0xff)
	return 0.2126*r+0.7152*g+0.0722*b > 128
}

// filter passes the program's output through, taking out the questions it
// answers. A sequence split across two reads is held until it is whole.
type filter struct {
	h      *host
	answer func(string)
	held   []byte
}

// maxHeld bounds how much of an unfinished sequence is held back. A colour
// question is a few bytes long; anything longer is not one and is passed on.
const maxHeld = 4096

func (f *filter) feed(p []byte) []byte {
	data := append(f.held, p...)
	f.held = nil
	out := make([]byte, 0, len(data))
	i := 0
	for i < len(data) {
		esc := bytes.IndexByte(data[i:], 0x1b)
		if esc < 0 {
			out = append(out, data[i:]...)
			break
		}
		out = append(out, data[i:i+esc]...)
		i += esc
		if i+1 >= len(data) {
			f.held = append(f.held, data[i:]...)
			break
		}
		switch data[i+1] {
		case ']':
			end, termLen := oscEnd(data[i+2:])
			if end < 0 {
				if len(data)-i > maxHeld {
					out = append(out, data[i:]...)
					i = len(data)
				} else {
					f.held = append(f.held, data[i:]...)
					i = len(data)
				}
				continue
			}
			body := string(data[i+2 : i+2+end])
			whole := data[i : i+2+end+termLen]
			if !f.oscQuery(body) {
				out = append(out, whole...)
			}
			i += len(whole)
		case '[':
			end := csiEnd(data[i+2:])
			if end < 0 {
				if len(data)-i > 64 {
					out = append(out, data[i:]...)
					i = len(data)
				} else {
					f.held = append(f.held, data[i:]...)
					i = len(data)
				}
				continue
			}
			whole := data[i : i+2+end+1]
			if !f.csi(string(data[i+2:i+2+end]), data[i+2+end]) {
				out = append(out, whole...)
			}
			i += len(whole)
		default:
			out = append(out, data[i])
			i++
		}
	}
	return out
}

// oscEnd finds the terminator of an OSC body: BEL or ESC \. It returns the
// body's length and the terminator's, or -1 when the body is not finished.
func oscEnd(b []byte) (int, int) {
	for j := 0; j < len(b); j++ {
		switch b[j] {
		case 0x07:
			return j, 1
		case 0x1b:
			if j+1 >= len(b) {
				return -1, 0
			}
			if b[j+1] == '\\' {
				return j, 2
			}
		}
	}
	return -1, 0
}

// csiEnd finds a CSI's final byte, or -1 when it has not arrived.
func csiEnd(b []byte) int {
	for j := 0; j < len(b); j++ {
		if b[j] >= 0x40 && b[j] <= 0x7e {
			return j
		}
	}
	return -1
}

// oscQuery answers a colour question and reports whether it did.
func (f *filter) oscQuery(body string) bool {
	s := f.h.now()
	if f.h.mute {
		if body == "10;?" || body == "11;?" || (strings.HasPrefix(body, "4;") && strings.HasSuffix(body, ";?")) {
			f.h.note("unanswered %s", body)
			return true
		}
		return false
	}
	switch {
	case body == "10;?":
		f.h.note("answer 10 %s", s.fg)
		f.answer("\x1b]10;" + xrgb(s.fg) + "\x1b\\")
		return true
	case body == "11;?":
		f.h.note("answer 11 %s", s.bg)
		f.answer("\x1b]11;" + xrgb(s.bg) + "\x1b\\")
		return true
	case strings.HasPrefix(body, "4;") && strings.HasSuffix(body, ";?"):
		n := strings.TrimSuffix(strings.TrimPrefix(body, "4;"), ";?")
		idx, err := strconv.Atoi(n)
		if err != nil {
			return false
		}
		f.h.mu.Lock()
		hex, ok := f.h.ansi[idx]
		f.h.mu.Unlock()
		if !ok {
			// Swallowed without an answer: a host that does not know a
			// slot says nothing, which is what tuios has to cope with.
			return true
		}
		f.h.note("answer 4;%d %s", idx, hex)
		f.answer("\x1b]4;" + n + ";" + xrgb(hex) + "\x1b\\")
		return true
	}
	return false
}

// csi answers the colour scheme query, and follows mode 2031. It reports
// whether the sequence was consumed.
func (f *filter) csi(params string, final byte) bool {
	switch {
	case params == "?996" && final == 'n':
		if f.h.mute {
			f.h.note("unanswered 996")
			return true
		}
		s := f.h.now()
		f.h.note("answer 996 light=%t", s.light)
		f.answer(dsr997(s.light))
		return true
	case params == "?2031" && (final == 'h' || final == 'l'):
		f.h.mu.Lock()
		f.h.subscribed = final == 'h'
		f.h.mu.Unlock()
		f.h.note("mode 2031 %c", final)
	}
	return false
}

// query asks the terminal on /dev/tty one colour question and prints the
// answer.
func query(spec string) int {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hostterm query: %v\n", err)
		return 1
	}
	defer tty.Close()
	old, err := term.MakeRaw(tty.Fd())
	if err != nil {
		fmt.Fprintf(os.Stderr, "hostterm query: %v\n", err)
		return 1
	}
	if _, err := tty.WriteString("\x1b]" + spec + ";?\x1b\\"); err != nil {
		_ = term.Restore(tty.Fd(), old)
		return 1
	}
	got := make(chan string, 1)
	go func() {
		var reply []byte
		buf := make([]byte, 256)
		for {
			n, err := tty.Read(buf)
			reply = append(reply, buf[:n]...)
			if end, _ := oscEnd(bytes.TrimPrefix(reply, []byte("\x1b]"))); end >= 0 || err != nil {
				got <- string(reply)
				return
			}
		}
	}()
	var reply string
	select {
	case reply = <-got:
	case <-time.After(3 * time.Second):
		reply = "no answer"
	}
	_ = term.Restore(tty.Fd(), old)
	reply = strings.TrimPrefix(reply, "\x1b]")
	reply = strings.TrimSuffix(strings.TrimSuffix(reply, "\x1b\\"), "\x07")
	// The answer repeats the question's own number; only the colour is
	// printed, so the line is the same for an ST and a BEL answer.
	if _, colour, ok := strings.Cut(strings.TrimPrefix(reply, spec), ";"); ok && strings.HasPrefix(reply, spec+";") {
		reply = colour
	}
	fmt.Printf("HOSTTERM-%s=%s\n", spec, reply)
	return 0
}
