package shot

import (
	"bytes"
	"compress/gzip"
	"embed"
	"io"
	"sync"
)

//go:embed fonts/*.ttf.gz
var goFontFiles embed.FS

var (
	goFontsOnce sync.Once
	goMonoTTF   []byte
	goMonoBold  []byte
	goFontsErr  error
)

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
