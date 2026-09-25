//go:build !js

// Package lexers holds the syntax definitions the review highlights with,
// and the registry that serves them.
//
// chroma's own lexers package embeds all 279 of its definitions, 2.4 MB of
// XML that made up a tenth of the tuios binary, and parses the header of
// every one at start. This package carries the languages a repository is
// likely to hold, gzipped, and registers them on first use. A file in any
// other language is drawn plain. gen.go writes the .xml.gz files and says
// how to change the list.
package lexers

import (
	"bytes"
	"compress/gzip"
	"embed"
	"io"
	"io/fs"
	"path"
	"sync"
	"time"

	"github.com/alecthomas/chroma/v2"
)

//go:embed *.xml.gz
var lexerFiles embed.FS

var (
	registryOnce sync.Once
	registry     *chroma.LexerRegistry
)

// Registry returns the registry of every lexer here, building it on first
// use. It is safe to call from any goroutine.
func Registry() *chroma.LexerRegistry {
	registryOnce.Do(func() {
		reg := chroma.NewLexerRegistry()
		src := gzipFS{lexerFiles}
		names, _ := fs.Glob(lexerFiles, "*.xml.gz")
		for _, name := range names {
			l, err := chroma.NewXMLLexer(src, name[:len(name)-len(".gz")])
			if err != nil {
				continue
			}
			reg.Register(l)
		}
		registerCodedLexers(reg)
		registry = reg
	})
	return registry
}

// gzipFS serves each name.xml as the decompressed name.xml.gz beside it.
// chroma opens a definition twice, once for its header and again for its
// rules on the first tokenise, so it reads through here both times.
type gzipFS struct{ fs.FS }

func (g gzipFS) Open(name string) (fs.File, error) {
	f, err := g.FS.Open(name + ".gz")
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // opened read-only: nothing to flush
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	data, err := io.ReadAll(zr)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	return &memFile{name: path.Base(name), Reader: bytes.NewReader(data)}, nil
}

// memFile is a decompressed definition.
type memFile struct {
	name string
	*bytes.Reader
}

func (f *memFile) Stat() (fs.FileInfo, error) { return f, nil }
func (f *memFile) Close() error               { return nil }

func (f *memFile) Name() string       { return f.name }
func (f *memFile) Size() int64        { return f.Reader.Size() }
func (f *memFile) Mode() fs.FileMode  { return 0o444 }
func (f *memFile) ModTime() time.Time { return time.Time{} }
func (f *memFile) IsDir() bool        { return false }
func (f *memFile) Sys() any           { return nil }
