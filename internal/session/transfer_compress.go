package session

import (
	"bytes"
	"compress/flate"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const compressSampleSize = 64 << 10

const compressMinSize = 512

const compressFastLink = 80 << 20

var incompressibleExt = map[string]bool{}

func init() {
	for ext := range strings.FieldsSeq("3g2 3gp 7z aac ace apk avi bz2 deb dmg ear f4v flac flv gpg gz iso jar jpeg jpg lrz lz lz4 lzma lzo m1a m1v m2a m2ts m2v m4a m4b m4p m4r m4v mka mkv mov mp1 mp2 mp3 mp4 mpa mpeg mpg mpv mts odb odf odg odi odm odp ods odt oga ogg ogm ogv ogx opus otg oth otp ots ott oxt png qt rar rpm rz rzip spx squashfs sxc sxd sxg sxm sxw sz tbz tbz2 tgz tlz ts txz tzo vob war webm webp wma wmv xz z zip zst docx xlsx pptx pdf heic heif avif whl crx xpi") {
		incompressibleExt["."+ext] = true
	}
}

func compressibleName(name string) bool {
	return !incompressibleExt[strings.ToLower(filepath.Ext(name))]
}

func compressibleSample(sample []byte) bool {
	if len(sample) < compressMinSize {
		return false
	}
	var c countWriter
	fw := getFlateWriter(&c)
	_, _ = fw.Write(sample)
	_ = fw.Close()
	putFlateWriter(fw)
	return c.n*10 < int64(len(sample))*9
}

func compressibleFile(f *os.File, name string, size, off int64) bool {
	if size < compressMinSize || !compressibleName(name) {
		return false
	}
	buf := make([]byte, min(size, compressSampleSize))
	n, err := f.ReadAt(buf, off)
	if err != nil && !errors.Is(err, io.EOF) {
		return false
	}
	return compressibleSample(buf[:n])
}

type countWriter struct{ n int64 }

func (c *countWriter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	return len(p), nil
}

var flateWriters = sync.Pool{New: func() any {
	fw, _ := flate.NewWriter(io.Discard, flate.BestSpeed)
	return fw
}}

func getFlateWriter(w io.Writer) *flate.Writer {
	fw := flateWriters.Get().(*flate.Writer)
	fw.Reset(w)
	return fw
}

func putFlateWriter(fw *flate.Writer) {
	fw.Reset(io.Discard)
	flateWriters.Put(fw)
}

var flateReaders sync.Pool

func getFlateReader(r io.Reader) io.ReadCloser {
	if fr, ok := flateReaders.Get().(io.ReadCloser); ok {
		if err := fr.(flate.Resetter).Reset(r, nil); err == nil {
			return fr
		}
	}
	return flate.NewReader(r)
}

func putFlateReader(fr io.ReadCloser) {
	flateReaders.Put(fr)
}

func flateEnd(fr io.Reader) error {
	var one [1]byte
	n, err := io.ReadFull(fr, one[:])
	switch {
	case n > 0:
		return errors.New("the compressed bytes hold more than the file's size")
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return nil
	}
	return err
}

func (j *transferJob) compressionWanted() bool {
	if j.opts.NoCompress {
		return false
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.rateLocked() < compressFastLink
}

func sampleReader(r io.Reader, size int64) ([]byte, io.Reader, error) {
	buf := make([]byte, min(size, compressSampleSize))
	n, err := io.ReadFull(r, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, nil, err
	}
	return buf[:n], io.MultiReader(bytes.NewReader(buf[:n]), r), nil
}

type partSums struct {
	mu sync.Mutex
	m  map[string]partSum
}

type partSum struct {
	sum   string
	size  int64
	mtime time.Time
}

const partSumsMax = 4096

func (p *partSums) put(path string, f *os.File, sum string) {
	fi, err := f.Stat()
	if err != nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.m == nil {
		p.m = map[string]partSum{}
	}
	if len(p.m) >= partSumsMax {
		for k := range p.m {
			delete(p.m, k)
			break
		}
	}
	p.m[path] = partSum{sum: sum, size: fi.Size(), mtime: fi.ModTime()}
}

func (p *partSums) take(fsys fileFS, path string) string {
	if p == nil {
		return ""
	}
	p.mu.Lock()
	s, ok := p.m[path]
	delete(p.m, path)
	p.mu.Unlock()
	if !ok {
		return ""
	}
	fi, err := fsys.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() != s.size || !fi.ModTime().Equal(s.mtime) {
		return ""
	}
	return s.sum
}
