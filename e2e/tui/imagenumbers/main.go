// Command imagenumbers stands in for a guest that names its kitty images by
// number (I=) and never by id (i=), the way cbirds --render kitty does:
// every image is transmitted once as a PNG under I=, and every frame is a
// synchronized update that deletes all placements (d=a) and places each image
// again by its number at a cursor position.
//
// Two images go out by number: I=7 is 40x40 pixels and I=9 is 30x60. One more,
// 20x20, goes out by id (i=5) and is placed by id, as a guest that uses ids
// does. After two frames the guest frees I=9 by number (d=N) and keeps the
// rest on screen.
//
// Nothing is sent with q= other than 2, as cbirds sends it, so no answer is
// expected.
package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func pngOf(w, h int, shade byte) string {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.SetNRGBA(x, y, color.NRGBA{R: shade, G: byte(x * 5), B: byte(y * 3), A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(buf.Bytes())
}

func main() {
	out := os.Stdout
	_, _ = fmt.Fprintf(out, "\x1b_Ga=t,q=2,f=100,I=7,m=0;%s\x1b\\", pngOf(40, 40, 200))
	_, _ = fmt.Fprintf(out, "\x1b_Ga=t,q=2,f=100,I=9,m=0;%s\x1b\\", pngOf(30, 60, 90))
	_, _ = fmt.Fprintf(out, "\x1b_Ga=t,q=2,f=100,i=5,m=0;%s\x1b\\", pngOf(20, 20, 40))

	for range 2 {
		_, _ = out.WriteString("\x1b[?2026h\x1b_Ga=d,d=a\x1b\\")
		_, _ = out.WriteString("\x1b[3;3H\x1b_Ga=p,I=7,q=2,X=1,Y=2,C=1\x1b\\")
		_, _ = out.WriteString("\x1b[3;20H\x1b_Ga=p,I=9,q=2,X=0,Y=0,C=1\x1b\\")
		_, _ = out.WriteString("\x1b[3;40H\x1b_Ga=p,i=5,q=2,C=1\x1b\\")
		_, _ = out.WriteString("\x1b[?2026l")
		time.Sleep(200 * time.Millisecond)
	}
	_, _ = out.WriteString("\x1b_Ga=d,d=N,I=9,q=2\x1b\\")
	_, _ = out.WriteString("\x1b[10;1HIMAGENUMBERS-DONE\r\n")

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	select {
	case <-stop:
	case <-time.After(60 * time.Second):
	}
}
