//go:build ignore

// gen.go packs files into a gzipped tar archive. Run it from a package's
// go:generate line:
//
//	go run ../tarpack/gen.go OUT.tar.gz PATH...
//
// A path that is a folder adds every file under it. Each file is stored under
// its path relative to the current folder, with forward slashes. The archive
// is deterministic: names in order, no times, no owners, so the same files give
// the same bytes on the same Go version.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"slices"
)

func main() {
	if len(os.Args) < 3 {
		log.Fatal("usage: gen.go OUT.tar.gz PATH...")
	}
	var paths []string
	for _, root := range os.Args[2:] {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				paths = append(paths, filepath.ToSlash(p))
			}
			return nil
		})
		if err != nil {
			log.Fatal(err)
		}
	}
	slices.Sort(paths)

	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		log.Fatal(err)
	}
	tw := tar.NewWriter(zw)
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			log.Fatal(err)
		}
		hdr := &tar.Header{
			Typeflag: tar.TypeReg,
			Name:     p,
			Mode:     0o644,
			Size:     int64(len(data)),
			Format:   tar.FormatUSTAR,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			log.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			log.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		log.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(os.Args[1], buf.Bytes(), 0o644); err != nil {
		log.Fatal(err)
	}
}
