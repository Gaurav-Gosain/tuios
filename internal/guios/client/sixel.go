package client

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"image"
	"strconv"

	"tuios.dev/vt"
)

// Sixel images.
//
// The pane's terminal (libghostty-vt in the terminal plugin) draws kitty
// images and has no sixel decoder. The daemon tells programs the pane draws
// sixel, as a sixel terminal would, and lays a sixel image out in its own
// emulator: it reserves the image's cells and leaves the cursor at column 0
// of the row under the image (vt.Emulator.placeSixel). So the stream turns
// each sixel image a program writes into the same picture as a kitty image,
// decoded by the decoder the TUI uses, and moves the cursor where the
// daemon's emulator moved it. The two terminals then keep the same grid.

// sixelMaxBody bounds the bytes of one sixel image held while it arrives. An
// image past it goes to the terminal as it came, which draws nothing.
const sixelMaxBody = 64 << 20

// sixelConv rewrites sixel images in a pane's output as kitty images. It
// keeps the state of an image split across output frames.
type sixelConv struct {
	// held is output not yet passed on: a trailing ESC that may start a
	// DCS, or a DCS whose kind is not known yet, or a sixel image that has
	// not ended. body says held is a sixel image ("ESC P params q ...").
	held []byte
	body bool
	// scanned is how far into held a sixel image was searched for its end,
	// so a large image is not searched again from its start each frame.
	scanned int
	// keep sees the output passed on, in order, and keeps each picture
	// for the next snapshot (images.go).
	keep imageKeep
}

// reset drops what is held, for a snapshot, which paints a fresh terminal.
func (c *sixelConv) reset() {
	c.held = nil
	c.body = false
	c.scanned = 0
	c.keep.reset()
}

// pass appends output passed on as it came, and shows it to keep.
func (c *sixelConv) pass(out, b []byte, cellW int) []byte {
	c.keep.observe(b, cellW)
	return append(out, b...)
}

// convert returns the output to pass on for in, with each complete sixel
// image as a kitty image. cellW and cellH are the pane's cell in pixels, the
// cell the daemon lays images out at.
func (c *sixelConv) convert(in []byte, cellW, cellH int) []byte {
	if len(c.held) == 0 && bytes.IndexByte(in, 0x1b) < 0 {
		c.keep.observe(in, cellW)
		return in
	}
	if len(c.held) == 0 && !bytes.Contains(in, []byte("\x1bP")) && in[len(in)-1] != 0x1b {
		c.keep.observe(in, cellW)
		return in
	}
	buf, owned, from := in, false, 2
	if len(c.held) > 0 {
		buf, owned = append(c.held, in...), true
		if c.body {
			from = max(2, c.scanned)
		}
		c.held, c.scanned = nil, 0
	}
	var out []byte
	for len(buf) > 0 {
		if !c.body {
			i := bytes.Index(buf, []byte("\x1bP"))
			if i < 0 {
				if buf[len(buf)-1] == 0x1b {
					out = c.pass(out, buf[:len(buf)-1], cellW)
					c.held = []byte{0x1b}
				} else {
					out = c.pass(out, buf, cellW)
				}
				return out
			}
			out = c.pass(out, buf[:i], cellW)
			buf = buf[i:]
			// The DCS is a sixel image when its parameters (digits and
			// semicolons) end in 'q', with no intermediate byte.
			j := 2
			for j < len(buf) && (buf[j] >= '0' && buf[j] <= '9' || buf[j] == ';') {
				j++
			}
			if j == len(buf) {
				c.held = append([]byte(nil), buf...)
				return out
			}
			if buf[j] != 'q' {
				out = c.pass(out, buf[:2], cellW)
				buf = buf[2:]
				continue
			}
			c.body, from = true, 2
		}
		// buf starts with "ESC P"; the image ends at ST (ESC \). Any other
		// ESC ends it too, and CAN or SUB cancel it, as they end a DCS.
		end, next, cancel := -1, 0, false
	scan:
		for k := from; k < len(buf); k++ {
			switch buf[k] {
			case 0x1b:
				if k+1 == len(buf) {
					break scan
				}
				end, next = k, k
				if buf[k+1] == '\\' {
					next = k + 2
				}
				break scan
			case 0x18, 0x1a:
				end, next, cancel = k, k+1, true
				break scan
			}
		}
		if end < 0 {
			if len(buf) > sixelMaxBody {
				// Too large to hold: the terminal gets it as it came.
				out = c.pass(out, buf, cellW)
				c.body = false
				return out
			}
			if !owned {
				buf = append([]byte(nil), buf...)
			}
			// The last byte may be an ESC that starts the end.
			c.held, c.scanned = buf, max(2, len(buf)-1)
			return out
		}
		if !cancel {
			at := len(out)
			var cols int
			out, cols = appendSixelAsKitty(out, buf[2:end], cellW, cellH)
			if n := bytes.LastIndex(out[at:], []byte("\x1b\\")); n >= 0 {
				// The kitty commands, without the cursor moves after.
				c.keep.keepSixel(out[at:at+n+2], cols)
			}
		}
		buf = buf[next:]
		c.body, from = false, 2
	}
	return out
}

// appendSixelAsKitty appends a kitty image for the sixel DCS body ("params q
// data"), placed at the cursor, then moves the cursor to column 0 of the row
// under the image, scrolling as needed, as the daemon's emulator does.
func appendSixelAsKitty(out, body []byte, cellW, cellH int) ([]byte, int) {
	cmd := vt.ParseSixelCommand(body)
	if cmd == nil {
		return out, 0
	}
	if cellW <= 0 || cellH <= 0 {
		cellW, cellH = vt.FallbackCellWidth, vt.FallbackCellHeight
	}
	rows, cols := vt.SixelCells(cmd, cellW, cellH)
	if rows <= 0 || cols <= 0 {
		return out, 0
	}
	if img := vt.DecodeSixel(cmd); img != nil {
		var z bytes.Buffer
		w := zlib.NewWriter(&z)
		_, _ = w.Write(img.RGBA(image.Rect(0, 0, img.Width, img.Height)))
		_ = w.Close()
		data := base64.StdEncoding.EncodeToString(z.Bytes())
		// No image id: the terminal names it, so it never meets an id a
		// program chose. q=2: a reply would go to the program.
		const chunk = 4096
		for i := 0; i == 0 || i < len(data); i += chunk {
			part := data[i:min(i+chunk, len(data))]
			more := byte('0')
			if i+chunk < len(data) {
				more = '1'
			}
			if i == 0 {
				out = append(out, "\x1b_Ga=T,f=32,o=z,C=1,q=2,s="...)
				out = strconv.AppendInt(out, int64(img.Width), 10)
				out = append(out, ",v="...)
				out = strconv.AppendInt(out, int64(img.Height), 10)
				out = append(out, ",m="...)
			} else {
				out = append(out, "\x1b_Gm="...)
			}
			out = append(out, more, ';')
			out = append(out, part...)
			out = append(out, "\x1b\\"...)
		}
	}
	out = append(out, '\r')
	for range rows {
		out = append(out, "\x1bD"...)
	}
	return out, cols
}
