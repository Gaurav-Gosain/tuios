package main

import (
	"bytes"
	"io"
	"regexp"
	"strconv"
	"time"
)

// rgb is a colour of the terminal's palette.
type rgb struct{ r, g, b uint8 }

func (c rgb) mix(o rgb, t float64) rgb {
	m := func(a, b uint8) uint8 { return uint8(float64(a) + (float64(b)-float64(a))*t + .5) }
	return rgb{m(c.r, o.r), m(c.g, o.g), m(c.b, o.b)}
}

func (c rgb) fg() string { return "38;2;" + c.str() }
func (c rgb) bg() string { return "48;2;" + c.str() }
func (c rgb) str() string {
	return strconv.Itoa(int(c.r)) + ";" + strconv.Itoa(int(c.g)) + ";" + strconv.Itoa(int(c.b))
}

// palette is the background, the text colour and the 16 colours of the
// terminal. A tuios pane answers with the colours of the tuios theme, so
// every shade the plugin draws comes from the theme.
type palette struct {
	bg, fg rgb
	ansi   [16]rgb
	known  bool
}

var oscColor = regexp.MustCompile(`\x1b\](10|11|4;(\d+));rgb:([0-9a-fA-F]+)/([0-9a-fA-F]+)/([0-9a-fA-F]+)(?:\x07|\x1b\\)`)

func hexByte(s string) uint8 {
	n, _ := strconv.ParseUint(s, 16, 32)
	switch len(s) {
	case 1:
		return uint8(n * 17)
	case 2:
		return uint8(n)
	case 3:
		return uint8(n >> 4)
	}
	return uint8(n >> 8)
}

// colorQueries asks for the text colour, the background and the 16
// colours, then for the device attributes, whose answer ends the answers.
func colorQueries() []byte {
	var q bytes.Buffer
	q.WriteString("\x1b]10;?\x07\x1b]11;?\x07")
	for i := range 16 {
		q.WriteString("\x1b]4;")
		q.WriteString(strconv.Itoa(i))
		q.WriteString(";?\x07")
	}
	q.WriteString("\x1b[c")
	return q.Bytes()
}

// queryPalette asks the terminal for its colours, and ends its wait at the
// answer to the device attributes query, which every terminal sends, or
// after the timeout. It gives the bytes that came after the answers, which
// are keys.
func queryPalette(out io.Writer, chunks <-chan []byte, wait time.Duration) (palette, []byte) {
	_, _ = out.Write(colorQueries())
	var p palette
	var got []byte
	da := regexp.MustCompile(`\x1b\[\?[0-9;]*c`)
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		select {
		case c, ok := <-chunks:
			if !ok {
				return p, nil
			}
			got = append(got, c...)
			if loc := da.FindIndex(got); loc != nil {
				parse(&p, got[:loc[0]])
				return p, got[loc[1]:]
			}
		case <-timer.C:
			// The rest of an answer may still come: the decoder takes what
			// came, so the tail of an answer is not read as keys.
			parse(&p, got)
			return p, got
		}
	}
}

func parse(p *palette, b []byte) {
	seen := 0
	for _, m := range oscColor.FindAllSubmatch(b, -1) {
		c := rgb{hexByte(string(m[3])), hexByte(string(m[4])), hexByte(string(m[5]))}
		switch {
		case string(m[1]) == "10":
			p.fg = c
		case string(m[1]) == "11":
			p.bg = c
		default:
			if i, err := strconv.Atoi(string(m[2])); err == nil && i < 16 {
				p.ansi[i] = c
			}
		}
		seen++
	}
	p.known = seen >= 18
}
