//go:build ignore

// gen.go packs manifests/*.toml into manifests.tar.gz, the archive the harness
// package embeds. Run it after editing a bundled manifest:
//
//	go generate ./internal/harness
//
// The archive is deterministic: names in order, no times, no owners, so the
// same files give the same bytes on the same Go version.
// TestBundledArchiveMatchesFiles fails when the archive and the files differ.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"log"
	"os"
	"path/filepath"
	"slices"
)

func main() {
	paths, err := filepath.Glob(filepath.Join("manifests", "*.toml"))
	if err != nil {
		log.Fatal(err)
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
			Name:     filepath.Base(p),
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
	if err := os.WriteFile("manifests.tar.gz", buf.Bytes(), 0o644); err != nil {
		log.Fatal(err)
	}
}
