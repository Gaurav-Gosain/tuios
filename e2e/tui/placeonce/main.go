// Command placeonce stands in for a guest that transmits an image once and
// then places it once, the way a compositor or an image viewer that separates
// the two steps does: a=t under an image id, then a single a=p naming that id
// at the cursor, with no cell count. Nothing is sent after that.
//
// Usage: placeonce TRANSPORT
//
// TRANSPORT is b64 (t=d), shm (t=s), png (t=d,f=100) or pngz (t=d,f=100,o=z).
// The image is 40x40 pixels. The png transports send no s= or v=, as a PNG
// sender usually does: the size is only in the PNG's own header.
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
	"os/signal"
	"syscall"
	"time"
)

const (
	imageID = 5
	width   = 40
	height  = 40
)

func main() {
	transport := "b64"
	if len(os.Args) > 1 && os.Args[1] != "" {
		transport = os.Args[1]
	}
	pix := make([]byte, width*height*4)
	for i := range pix {
		pix[i] = byte(i * 13)
	}

	var transmit string
	switch transport {
	case "shm":
		// The harness names the object with a prefix of its own, so it can
		// remove it after a test even when this process dies by a signal and
		// the deferred removal never runs.
		prefix := os.Getenv("TUIOS_E2E_SHM_PREFIX")
		if prefix == "" {
			prefix = "tuios-placeonce-"
		}
		name := fmt.Sprintf("%s%d", prefix, os.Getpid())
		path := "/dev/shm/" + name
		if err := os.WriteFile(path, pix, 0o600); err != nil {
			fmt.Printf("PLACEONCE-ERR %v\n", err)
			return
		}
		defer func() { _ = os.Remove(path) }()
		transmit = fmt.Sprintf("\x1b_Ga=t,t=s,f=32,s=%d,v=%d,i=%d,q=2;%s\x1b\\",
			width, height, imageID, base64.StdEncoding.EncodeToString([]byte(name)))
	case "png", "pngz":
		img := image.NewNRGBA(image.Rect(0, 0, width, height))
		for y := range height {
			for x := range width {
				img.SetNRGBA(x, y, color.NRGBA{R: byte(x * 6), G: byte(y * 6), B: 128, A: 255})
			}
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			fmt.Printf("PLACEONCE-ERR %v\n", err)
			return
		}
		payload, extra := buf.Bytes(), ""
		if transport == "pngz" {
			var z bytes.Buffer
			zw := zlib.NewWriter(&z)
			_, _ = zw.Write(payload)
			_ = zw.Close()
			payload, extra = z.Bytes(), ",o=z"
		}
		transmit = fmt.Sprintf("\x1b_Ga=t,t=d,f=100%s,i=%d,q=2;%s\x1b\\",
			extra, imageID, base64.StdEncoding.EncodeToString(payload))
	default:
		transmit = fmt.Sprintf("\x1b_Ga=t,t=d,f=32,s=%d,v=%d,i=%d,q=2;%s\x1b\\",
			width, height, imageID, base64.StdEncoding.EncodeToString(pix))
	}

	_, _ = os.Stdout.WriteString(transmit)
	// The placement names the image and nothing else: no c=, no r=. The size
	// has to come from the transmission above.
	_, _ = fmt.Fprintf(os.Stdout, "\x1b_Ga=p,i=%d,p=1,C=1,q=2\x1b\\", imageID)
	_, _ = os.Stdout.WriteString("\r\n\r\n\r\nPLACEONCE-DONE\r\n")

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	select {
	case <-stop:
	case <-time.After(60 * time.Second):
	}
}
