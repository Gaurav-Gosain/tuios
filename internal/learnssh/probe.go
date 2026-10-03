package learnssh

import (
	"bytes"
	"io"
	"regexp"
	"strings"
	"time"
)

// da1Reply is a terminal's answer to "\x1b[c".
var da1Reply = regexp.MustCompile(`\x1b\[\?[0-9;]*c`)

// probeColor works out the client's colour depth, and starts reading its
// input. It returns "truecolor", "256" or "16", and the input as chunks.
//
// SSH clients rarely send COLORTERM, so the terminal is asked: XTGETTCAP for
// "RGB", then DA1, which every terminal answers. A terminal that knows RGB
// answers the first before the second. One that answers only DA1, or
// neither within the wait, is judged by its TERM. The answers are taken out
// of the input; any keys typed during the wait go on to the tour.
func probeColor(sess io.ReadWriter, term string, env []string) (string, <-chan []byte) {
	in := make(chan []byte, 32)
	go func() {
		defer close(in)
		for {
			buf := make([]byte, 4096)
			n, err := sess.Read(buf)
			if n > 0 {
				in <- buf[:n]
			}
			if err != nil {
				return
			}
		}
	}()

	guess := colorFromTerm(term, env)
	if _, err := io.WriteString(sess, "\x1bP+q524742\x1b\\\x1b[c"); err != nil {
		return guess, in
	}
	var got []byte
	timeout := time.After(500 * time.Millisecond)
	for {
		select {
		case b, ok := <-in:
			if !ok {
				return guess, in
			}
			got = append(got, b...)
			if loc := da1Reply.FindIndex(got); loc != nil {
				before := got[:loc[0]]
				rest := got[loc[1]:]
				color := guess
				if bytes.Contains(before, []byte("\x1bP1+r")) {
					color = "truecolor"
				}
				// Drop the XTGETTCAP answer, keep anything else typed.
				if i := bytes.Index(before, []byte("\x1bP")); i >= 0 {
					if j := bytes.Index(before[i:], []byte("\x1b\\")); j >= 0 {
						before = append(before[:i:i], before[i+j+2:]...)
					}
				}
				return color, prepend(append(before, rest...), in)
			}
		case <-timeout:
			return guess, prepend(got, in)
		}
	}
}

// prepend puts b in front of the rest of the input.
func prepend(b []byte, in <-chan []byte) <-chan []byte {
	if len(b) == 0 {
		return in
	}
	out := make(chan []byte, 32)
	go func() {
		defer close(out)
		out <- b
		for c := range in {
			out <- c
		}
	}()
	return out
}

// colorFromTerm guesses the depth from TERM and COLORTERM alone.
func colorFromTerm(term string, env []string) string {
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "COLORTERM="); ok && (v == "truecolor" || v == "24bit") {
			return "truecolor"
		}
	}
	t := strings.ToLower(term)
	switch {
	case strings.Contains(t, "direct"), strings.Contains(t, "truecolor"), strings.Contains(t, "24bit"),
		strings.Contains(t, "kitty"), strings.Contains(t, "ghostty"), strings.Contains(t, "wezterm"),
		strings.Contains(t, "alacritty"), strings.HasPrefix(t, "foot"), strings.Contains(t, "contour"),
		strings.Contains(t, "rio"):
		return "truecolor"
	case t == "linux", t == "vt100", t == "vt220", t == "ansi", strings.HasPrefix(t, "screen") && !strings.Contains(t, "256"):
		return "16"
	}
	return "256"
}
