// Command oneaxis stands in for a guest that states part of its cell box: it
// transmits a PNG and displays it with one a=T, naming its columns, its rows,
// both, or neither.
//
// Usage: oneaxis MODE [WxH]
//
// MODE is width (c=12), height (r=4), both, none, crop (c=12 over the left
// half of the picture), or place: the last transmits with a=t and then places
// with c=12, the other path a guest can take. WxH is
// the picture's size, 512x512 unless given.
//
// It prints ONE-AXIS-DONE once the image is on the wire and then holds, so the
// refresh passes that follow have a placement to re-place.
package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	imageID = 5
	cols    = 12
	rows    = 4
)

func main() {
	mode := "width"
	if len(os.Args) > 1 && os.Args[1] != "" {
		mode = os.Args[1]
	}
	spec := ""
	if len(os.Args) > 2 {
		spec = os.Args[2]
	}
	width, height, err := parseSize(spec)
	if err != nil {
		fmt.Printf("ONE-AXIS-ERR %v\n", err)
		return
	}

	var params string
	action := "T"
	switch mode {
	case "width":
		params = fmt.Sprintf(",c=%d", cols)
	case "height":
		params = fmt.Sprintf(",r=%d", rows)
	case "both":
		params = fmt.Sprintf(",c=%d,r=%d", cols, rows)
	case "crop":
		// The left half of the picture: the fitted axis follows its aspect,
		// not the whole picture's.
		params = fmt.Sprintf(",c=%d,x=0,y=0,w=%d,h=%d", cols, width/2, height)
	case "none":
	case "place":
		action = "t"
	default:
		fmt.Printf("ONE-AXIS-ERR unknown mode %q\n", mode)
		return
	}

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			nx := float64(2*x-width) / float64(width)
			ny := float64(2*y-height) / float64(height)
			if nx*nx+ny*ny < 0.9 {
				img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 0x80, A: 0xff})
				continue
			}
			img.Set(x, y, color.RGBA{R: 0x10, G: 0x10, B: 0x18, A: 0xff})
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		fmt.Printf("ONE-AXIS-ERR %v\n", err)
		return
	}
	encoded := base64.StdEncoding.EncodeToString(buf.Bytes())

	os.Stdout.WriteString("\x1b[H")
	first := true
	for len(encoded) > 0 {
		chunk := encoded
		if len(chunk) > 4096 {
			chunk = chunk[:4096]
		}
		encoded = encoded[len(chunk):]
		more := 0
		if len(encoded) > 0 {
			more = 1
		}
		if first {
			fmt.Printf("\x1b_Ga=%s,f=100,i=%d,t=d,q=2%s,m=%d;", action, imageID, params, more)
			first = false
		} else {
			fmt.Printf("\x1b_Gm=%d;", more)
		}
		os.Stdout.WriteString(chunk)
		os.Stdout.WriteString("\x1b\\")
	}
	if action == "t" {
		fmt.Printf("\x1b_Ga=p,i=%d,c=%d,C=1,q=2\x1b\\", imageID, cols)
	}
	fmt.Println("ONE-AXIS-DONE")
	time.Sleep(30 * time.Second)
}

// parseSize reads WxH, defaulting to a 512px square.
func parseSize(spec string) (width, height int, err error) {
	if spec == "" {
		return 512, 512, nil
	}
	w, h, ok := strings.Cut(strings.ToLower(spec), "x")
	if !ok {
		return 0, 0, fmt.Errorf("size %q is not WxH", spec)
	}
	if width, err = strconv.Atoi(w); err != nil || width <= 0 {
		return 0, 0, fmt.Errorf("size %q has no width", spec)
	}
	if height, err = strconv.Atoi(h); err != nil || height <= 0 {
		return 0, 0, fmt.Errorf("size %q has no height", spec)
	}
	return width, height, nil
}
