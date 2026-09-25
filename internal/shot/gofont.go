package shot

import (
	"bytes"
	"compress/gzip"
	"embed"
	"io"
	"sync"
)

// Go Mono and Go Mono Bold, the faces a PNG falls back to, gzipped: 157 KB
// in the binary where the gofont packages' []byte literals were 350 KB.
// fonts/gen.go writes them from the golang.org/x/image in go.mod, and
// fonts/README is their licence.

//go:embed fonts/*.ttf.gz
var goFontFiles embed.FS

var (
	goFontsOnce sync.Once
	goMonoTTF   []byte
	goMonoBold  []byte
	goFontsErr  error
)

// goMonoFonts returns the regular and bold TTF data, inflating them on the
// first call. The slices are shared and must not be written to.
func goMonoFonts() (regular, bold []byte, err error) {
	goFontsOnce.Do(func() {
		if goMonoTTF, goFontsErr = inflateFont("fonts/Go-Mono.ttf.gz"); goFontsErr != nil {
			return
		}
		goMonoBold, goFontsErr = inflateFont("fonts/Go-Mono-Bold.ttf.gz")
	})
	return goMonoTTF, goMonoBold, goFontsErr
}

func inflateFont(name string) ([]byte, error) {
	data, err := goFontFiles.ReadFile(name)
	if err != nil {
		return nil, err
	}
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(zr)
}
