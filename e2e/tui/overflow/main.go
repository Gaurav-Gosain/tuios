// Command overflow stands in for a guest that places a PNG past the bottom
// right corner of its pane, scaled into a cell box it names, the way an editor
// draws a diagram into a float. The picture is 2000x400 pixels in 100x10
// cells: 20 pixels a column and 40 a row, twice the 10x20 cell the tests pin,
// so a crop measured in the host's cell instead of in the picture's own scale
// is half the size it should be.
//
// Usage: OVERFLOW_DIR=DIR overflow MEDIUM [later]
//
// A file medium is written under DIR, the test's own temporary directory, so
// nothing is left behind in /tmp. Without it the system's is used.
//
// MEDIUM is how the picture travels:
//
//	direct     the bytes inline (t=d)
//	file       a file the host reads (t=f)
//	file-zlib  a file holding the PNG compressed again (t=f,o=z)
//	file-at    a file holding the PNG after other bytes (t=f,O=,S=)
//	temp       a temporary file the host deletes (t=t)
//
// It switches to the alternate screen, puts the cursor 41 columns from the
// right edge and 4 rows from the bottom, places the picture there with C=1,
// prints OVERFLOW-DONE and holds, so the refresh passes have a placement to
// re-place. With later it sends the picture with a=t, which stores it and
// shows nothing, and then places it with a=p, as snacks.nvim does.
package main

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"time"
)

const (
	imageID = 7
	width   = 2000
	height  = 400
	cols    = 100
	rows    = 10
)

func main() {
	medium := "direct"
	if len(os.Args) > 1 {
		medium = os.Args[1]
	}
	later := len(os.Args) > 2 && os.Args[2] == "later"
	data, err := picture()
	if err != nil {
		fmt.Printf("OVERFLOW-ERR %v\n", err)
		return
	}

	// Bottom right, then back to 41 columns and 4 rows of the picture showing.
	os.Stdout.WriteString("\x1b[?1049h\x1b[999;999H\x1b[3A\x1b[40D")
	place := fmt.Sprintf("a=T,f=100,i=%d,c=%d,r=%d,C=1,q=2", imageID, cols, rows)
	if later {
		place = fmt.Sprintf("a=t,f=100,i=%d,q=2", imageID)
	}
	switch medium {
	case "direct":
		sendDirect(place, data)
	case "file", "file-zlib", "file-at", "temp":
		params, err := writeMedium(medium, data)
		if err != nil {
			fmt.Printf("\x1b[?1049lOVERFLOW-ERR %v\n", err)
			return
		}
		fmt.Printf("\x1b_G%s%s\x1b\\", place, params)
	default:
		fmt.Printf("\x1b[?1049lOVERFLOW-ERR unknown medium %q\n", medium)
		return
	}
	if later {
		fmt.Printf("\x1b_Ga=p,i=%d,c=%d,r=%d,C=1,q=2\x1b\\", imageID, cols, rows)
	}
	os.Stdout.WriteString("\x1b[HOVERFLOW-DONE")
	time.Sleep(60 * time.Second)
}

// writeMedium writes the picture where the medium says and returns the rest of
// the command: the medium's own keys and the base64 path.
func writeMedium(medium string, data []byte) (string, error) {
	pattern := "overflow-*.png"
	if medium == "temp" {
		pattern = "tty-graphics-protocol-*.png"
	}
	f, err := os.CreateTemp(os.Getenv("OVERFLOW_DIR"), pattern)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	keys := ",t=f"
	switch medium {
	case "file-zlib":
		var z bytes.Buffer
		zw := zlib.NewWriter(&z)
		if _, err := zw.Write(data); err != nil {
			return "", err
		}
		if err := zw.Close(); err != nil {
			return "", err
		}
		data = z.Bytes()
		keys += ",o=z"
	case "file-at":
		lead := bytes.Repeat([]byte("not the picture "), 64)
		if _, err := f.Write(lead); err != nil {
			return "", err
		}
		keys += fmt.Sprintf(",O=%d,S=%d", len(lead), len(data))
	case "temp":
		keys = ",t=t"
	}
	if _, err := f.Write(data); err != nil {
		return "", err
	}
	return keys + ";" + base64.StdEncoding.EncodeToString([]byte(f.Name())), nil
}

func sendDirect(place string, data []byte) {
	encoded := base64.StdEncoding.EncodeToString(data)
	first := true
	for len(encoded) > 0 {
		chunk := encoded[:min(len(encoded), 4096)]
		encoded = encoded[len(chunk):]
		more := 0
		if len(encoded) > 0 {
			more = 1
		}
		if first {
			fmt.Printf("\x1b_G%s,t=d,m=%d;", place, more)
			first = false
		} else {
			fmt.Printf("\x1b_Gm=%d;", more)
		}
		os.Stdout.WriteString(chunk + "\x1b\\")
	}
}

// picture is a PNG of the stand-in's size with a gradient, so it is not a flat
// colour a host could draw at any scale and look the same.
func picture() ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 0x80, A: 0xff})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
