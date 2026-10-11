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

// Compression of a copy's bytes on a link.
//
// A copy that crosses a link may send a file's bytes as one flate stream, at
// BestSpeed. flate is in the standard library and already in the binary, so
// it costs no size; zstd would cost several hundred KB. A file is compressed
// only when it is worth it: not when its name says it is compressed already
// (the rsync skip list), and not when a sample of its first 64 KiB saves
// less than a tenth. The machine that reads the file decides, because it can
// read the sample without sending it.
//
// flate at BestSpeed runs at about 100 MB/s a core. A link faster than that is
// slowed by it, so a copy whose bytes already move faster stops compressing
// (compressionWanted).

// compressSampleSize is how much of a file the compression test reads.
const compressSampleSize = 64 << 10

// compressMinSize is the smallest file worth a flate stream: below it the
// stream's own bytes eat what it saves.
const compressMinSize = 512

// compressFastLink is the rate above which a copy stops compressing new
// files, in bytes a second.
const compressFastLink = 80 << 20

// incompressibleExt is the rsync skip list: files whose format compresses
// them already.
var incompressibleExt = map[string]bool{}

func init() {
	for ext := range strings.FieldsSeq("3g2 3gp 7z aac ace apk avi bz2 deb dmg ear f4v flac flv gpg gz iso jar jpeg jpg lrz lz lz4 lzma lzo m1a m1v m2a m2ts m2v m4a m4b m4p m4r m4v mka mkv mov mp1 mp2 mp3 mp4 mpa mpeg mpg mpv mts odb odf odg odi odm odp ods odt oga ogg ogm ogv ogx opus otg oth otp ots ott oxt png qt rar rpm rz rzip spx squashfs sxc sxd sxg sxm sxw sz tbz tbz2 tgz tlz ts txz tzo vob war webm webp wma wmv xz z zip zst docx xlsx pptx pdf heic heif avif whl crx xpi") {
		incompressibleExt["."+ext] = true
	}
}

// compressibleName reports whether a file's name leaves it worth compressing.
func compressibleName(name string) bool {
	return !incompressibleExt[strings.ToLower(filepath.Ext(name))]
}

// compressibleSample reports whether flate at BestSpeed saves a tenth of the
// sample or more.
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

// compressibleFile reports whether size bytes of f from off are worth a flate
// stream: the name, then a sample read with ReadAt, so f's offset stays.
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

// countWriter counts what is written to it.
type countWriter struct{ n int64 }

func (c *countWriter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	return len(p), nil
}

var flateWriters = sync.Pool{New: func() any {
	fw, _ := flate.NewWriter(io.Discard, flate.BestSpeed)
	return fw
}}

// getFlateWriter is a BestSpeed flate writer to w, from a pool: a new one
// allocates its whole window, and a folder copy writes one per file.
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

// getFlateReader is a flate reader of r, from a pool. r must be a
// bufio.Reader or another io.ByteReader, so the reader takes no byte past the
// end of the stream and the next record can be read after it.
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

// flateEnd reads a flate stream to its end after the bytes a copy expected,
// which must be all of it: a stream that holds more is not the file it says.
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

// compressionWanted reports whether a copy compresses the next file it sends
// over a link: not when the person turned it off, and not once its bytes move
// faster than flate.
func (j *transferJob) compressionWanted() bool {
	if j.opts.NoCompress {
		return false
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.rateLocked() < compressFastLink
}

// sampleReader reads the first part of a body to decide on compression, and
// gives the whole body back after it.
func sampleReader(r io.Reader, size int64) ([]byte, io.Reader, error) {
	buf := make([]byte, min(size, compressSampleSize))
	n, err := io.ReadFull(r, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, nil, err
	}
	return buf[:n], io.MultiReader(bytes.NewReader(buf[:n]), r), nil
}

// ---- hashes taken while the bytes landed -----------------------------------

// partSums holds the sha256 of each part a write stream hashed as its bytes
// landed, by the part's path, so file-commit does not read the part again.
// An entry counts only while the part is the size and age it had when it was
// hashed: anything that wrote to the part since makes the commit read it.
type partSums struct {
	mu sync.Mutex
	m  map[string]partSum
}

type partSum struct {
	sum   string
	size  int64
	mtime time.Time
}

// partSumsMax bounds the hashes held. A part whose hash was dropped is read
// again at its commit, which costs time and nothing else.
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

// take returns the hash of the part at path and forgets it, or "" when there
// is none or the part changed since.
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
