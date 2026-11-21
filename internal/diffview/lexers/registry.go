//go:build !js

// Package lexers holds the syntax definitions the review highlights with,
// and the registry that serves them.
//
// chroma's own lexers package embeds all 279 of its definitions, 2.4 MB of
// XML that made up a tenth of the tuios binary, and parses the header of
// every one at start. This package carries the languages a repository is
// likely to hold, compressed, and registers them on first use. A file in any
// other language is drawn plain. gen.go writes the .xml.z files and dict.z,
// and says how to change the list.
package lexers

import (
	"bytes"
	"compress/flate"
	"embed"
	"io"
	"io/fs"
	"path"
	"sync"
	"time"

	"github.com/alecthomas/chroma/v2"
)

//go:embed *.xml.z dict.z
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
		src := &deflateFS{FS: lexerFiles}
		src.dict, _ = inflate(lexerFiles, "dict.z", nil)
		names, _ := fs.Glob(lexerFiles, "*.xml.z")
		for _, name := range names {
			l, err := chroma.NewXMLLexer(src, name[:len(name)-len(".z")])
			if err != nil {
				continue
			}
			reg.Register(l)
		}
		registerCodedLexers(reg, src)
		src.dict = nil
		registry = reg
	})
	return registry
}

type deflateFS struct {
	fs.FS
	dict []byte
}

func (g *deflateFS) Open(name string) (fs.File, error) {
	dict := g.dict
	if dict == nil {
		var err error
		if dict, err = inflate(g.FS, "dict.z", nil); err != nil {
			return nil, &fs.PathError{Op: "open", Path: name, Err: err}
		}
	}
	data, err := inflate(g.FS, name+".z", dict)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: err}
	}
	return &memFile{name: path.Base(name), Reader: bytes.NewReader(data)}, nil
}

func inflate(fsys fs.FS, name string, dict []byte) ([]byte, error) {
	f, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	zr := flate.NewReaderDict(f, dict)
	defer func() { _ = zr.Close() }()
	return io.ReadAll(zr)
}

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
