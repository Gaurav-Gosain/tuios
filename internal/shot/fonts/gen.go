//go:build ignore

// gen writes Go Mono and Go Mono Bold, gzipped, from the golang.org/x/image
// version in go.mod. Run it from the repository root after changing that
// version:
//
//	go run ./internal/shot/fonts/gen.go
//
// The gofont packages hold each face as a []byte literal, 350 KB for the
// two that PNG screenshots use; gzipped they are 157 KB, and they are
// inflated on the first PNG. README is the fonts' licence.
package main

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"os"

	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/gomonobold"
)

func main() {
	for name, ttf := range map[string][]byte{
		"Go-Mono.ttf.gz":      gomono.TTF,
		"Go-Mono-Bold.ttf.gz": gomonobold.TTF,
	} {
		var buf bytes.Buffer
		zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		if err != nil {
			panic(err)
		}
		// No name and no time in the header, so a rerun writes the same bytes.
		if _, err := zw.Write(ttf); err != nil {
			panic(err)
		}
		if err := zw.Close(); err != nil {
			panic(err)
		}
		if err := os.WriteFile("internal/shot/fonts/"+name, buf.Bytes(), 0o644); err != nil {
			panic(err)
		}
		fmt.Printf("%s: %d -> %d bytes\n", name, len(ttf), buf.Len())
	}
}
