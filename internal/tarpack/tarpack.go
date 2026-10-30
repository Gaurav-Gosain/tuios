// Package tarpack reads the gzipped tar archives gen.go writes, for text a
// package embeds and rarely reads. Each archive is unpacked on first use, so a
// program that never reads it pays only for the bytes in the file.
package tarpack

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
)

// Unpack returns every file in the archive, by the name gen.go stored. It
// fails on an archive that is cut short or is not gzip.
func Unpack(archive []byte) (map[string][]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(zr)
	files := map[string][]byte{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return files, nil
		}
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		files[h.Name] = data
	}
}
