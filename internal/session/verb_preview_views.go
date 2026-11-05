package session

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/gif"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/gitstate"
	"github.com/Gaurav-Gosain/tuios/internal/review"
)

const (
	// previewJSONMax is the largest JSON file that is laid out for reading.
	previewJSONMax = 1 << 20
	// previewJSONOutMax is the largest laid-out JSON, since deep nesting makes the
	// layout far larger than the file.
	previewJSONOutMax = 16 << 20
	// previewHexMax is the start of a binary file a preview sends.
	previewHexMax = 16 << 10
	// previewArchiveItems and previewArchiveBytes bound a listing. A small gzip
	// can unpack to a great deal, and tar has to read through all of it.
	previewArchiveItems = 2000
	previewArchiveBytes = 1 << 30
	previewArchiveTime  = 3 * time.Second
	// previewArchiveName is the longest name a listing sends, in bytes.
	previewArchiveName = 4096
	// previewZipBuf is the read buffer for a zip's central directory.
	previewZipBuf = 64 << 10
	// previewDiffMax is the most of a diff a preview sends.
	previewDiffMax = 256 << 10
	// previewGIFFrames is the most frames of a GIF that can be stepped through.
	previewGIFFrames = 64
	// previewGIFCanvases is the memory held besides the frames, in paletted-frame units: two
	// RGBA pictures (the canvas and the copy a frame disposed to the previous one keeps), each
	// at 4 bytes per pixel, so 2*4 = 8.
	previewGIFCanvases = 8
)

// PreviewArchiveEntry is one row of an archive's listing.
type PreviewArchiveEntry struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	Dir  bool   `json:"dir,omitempty"`
}

// previewIsArchive reports whether the name is an archive tuios reads itself.
func previewIsArchive(path string) bool {
	name := strings.ToLower(path)
	for _, ext := range []string{".tar", ".tar.gz", ".tgz", ".zip"} {
		if strings.HasSuffix(name, ext) {
			return true
		}
	}
	return false
}

// previewArchive sends the listing of an archive, and reports false when the file
// cannot be read as one, so the caller shows it as a binary file.
func previewArchive(ctx context.Context, f *os.File, size int64, path string, out map[string]any) bool {
	ctx, cancel := context.WithTimeout(ctx, previewArchiveTime)
	defer cancel()
	name := strings.ToLower(path)
	var entries []PreviewArchiveEntry
	capped := false
	var err error
	if strings.HasSuffix(name, ".zip") {
		entries, capped, err = previewZip(f, size)
	} else {
		var src io.Reader = io.NewSectionReader(f, 0, size)
		if !strings.HasSuffix(name, ".tar") {
			var gz *gzip.Reader
			if gz, err = gzip.NewReader(src); err != nil {
				return false
			}
			defer func() { _ = gz.Close() }()
			src = gz
		}
		entries, capped, err = previewTar(ctx, io.LimitReader(src, previewArchiveBytes))
	}
	if err != nil && len(entries) == 0 {
		return false
	}
	out["kind"] = "archive"
	out["entries"] = entries
	out["capped"] = capped || err != nil
	return true
}

// previewTar lists a tar stream. A listing cut by a bound is still a listing.
func previewTar(ctx context.Context, src io.Reader) ([]PreviewArchiveEntry, bool, error) {
	var entries []PreviewArchiveEntry
	tr := tar.NewReader(src)
	for len(entries) < previewArchiveItems {
		if ctx.Err() != nil {
			return entries, true, nil
		}
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return entries, false, nil
		}
		if err != nil {
			return entries, true, err
		}
		entries = append(entries, PreviewArchiveEntry{Name: previewArchiveClip(h.Name), Size: h.Size, Dir: h.Typeflag == tar.TypeDir})
	}
	return entries, true, nil
}

// previewArchiveClip cuts a name longer than previewArchiveName at a rune boundary and
// marks the cut with "...". The result is a new string, so a long name is not kept alive.
func previewArchiveClip(name string) string {
	if len(name) <= previewArchiveName {
		return name
	}
	cut := previewArchiveName - len("...")
	for cut > 0 && !utf8.RuneStart(name[cut]) {
		cut--
	}
	return name[:cut] + "..."
}

// previewZip lists a zip file from its central directory, with nothing unpacked. It
// reads the first previewArchiveItems headers only, where archive/zip reads them all.
func previewZip(f *os.File, size int64) ([]PreviewArchiveEntry, bool, error) {
	records, base, off, err := previewZipEnd(f, size)
	if err != nil {
		return nil, false, err
	}
	buf := bufio.NewReaderSize(io.NewSectionReader(f, base+off, size-base-off), previewZipBuf)
	var entries []PreviewArchiveEntry
	capped := false
	for {
		h, err := previewZipHeader(buf)
		if err == zip.ErrFormat || err == io.ErrUnexpectedEOF {
			// The end of the directory; the count is only kept modulo 65536.
			if uint16(len(entries)) != uint16(records) {
				return nil, false, err
			}
			break
		}
		if err != nil {
			return nil, false, err
		}
		if len(entries) == previewArchiveItems {
			capped = true
			break
		}
		entries = append(entries, PreviewArchiveEntry{Name: previewArchiveClip(h.Name), Size: int64(h.UncompressedSize64), Dir: h.FileInfo().IsDir()})
	}
	return entries, capped, nil
}

// previewZipEnd finds the central directory from the end record, or the zip64 end record
// when that is present. It returns the entry count, the start of the zip proper in the
// file (a self-extracting archive has data before it) and the directory offset, as
// archive/zip does.
func previewZipEnd(r io.ReaderAt, size int64) (records uint64, base, off int64, err error) {
	var buf []byte
	var endAt int64
	for i, n := range []int64{1024, 65 * 1024} {
		n = min(n, size)
		buf = make([]byte, n)
		if _, err := r.ReadAt(buf, size-n); err != nil && err != io.EOF {
			return 0, 0, 0, err
		}
		if p := previewZipFindEnd(buf); p >= 0 {
			buf = buf[p:]
			endAt = size - n + int64(p)
			break
		}
		if i == 1 || n == size {
			return 0, 0, 0, zip.ErrFormat
		}
	}
	records = uint64(binary.LittleEndian.Uint16(buf[10:]))
	dirSize := uint64(binary.LittleEndian.Uint32(buf[12:]))
	dirOff := uint64(binary.LittleEndian.Uint32(buf[16:]))
	if int(binary.LittleEndian.Uint16(buf[20:])) > len(buf)-previewZipEndLen {
		return 0, 0, 0, errors.New("zip: invalid comment length")
	}
	if records == 0xffff || dirSize == 0xffffffff || dirOff == 0xffffffff {
		// A zip64 locator sits just before the end record and names the zip64 end record.
		if loc := endAt - 20; loc >= 0 {
			lb := make([]byte, 20)
			if _, err := r.ReadAt(lb, loc); err != nil {
				return 0, 0, 0, err
			}
			if binary.LittleEndian.Uint32(lb) == 0x07064b50 && binary.LittleEndian.Uint32(lb[4:]) == 0 && binary.LittleEndian.Uint32(lb[16:]) == 1 {
				at := int64(binary.LittleEndian.Uint64(lb[8:]))
				eb := make([]byte, 56)
				if _, err := r.ReadAt(eb, at); err != nil {
					return 0, 0, 0, err
				}
				if binary.LittleEndian.Uint32(eb) != 0x06064b50 {
					return 0, 0, 0, zip.ErrFormat
				}
				endAt = at
				records = binary.LittleEndian.Uint64(eb[32:])
				dirSize = binary.LittleEndian.Uint64(eb[40:])
				dirOff = binary.LittleEndian.Uint64(eb[48:])
			}
		}
	}
	if dirSize > 1<<63-1 || dirOff > 1<<63-1 {
		return 0, 0, 0, zip.ErrFormat
	}
	base = endAt - int64(dirSize) - int64(dirOff)
	if o := base + int64(dirOff); o < 0 || o >= size {
		return 0, 0, 0, zip.ErrFormat
	}
	// A prefix that the end record does not account for gives a bad base; when a
	// directory header is found at the plain offset, that offset is the right one.
	if base > 0 {
		d := int64(dirOff)
		if _, err := previewZipHeader(io.NewSectionReader(r, d, size-d)); err == nil {
			base = 0
		}
	}
	return records, base, int64(dirOff), nil
}

// previewZipEndLen is the length of a zip end record without its comment.
const previewZipEndLen = 22

// previewZipFindEnd returns where the last end record starts in b, or -1.
func previewZipFindEnd(b []byte) int {
	for i := len(b) - previewZipEndLen; i >= 0; i-- {
		if b[i] == 'P' && b[i+1] == 'K' && b[i+2] == 0x05 && b[i+3] == 0x06 {
			n := int(b[i+previewZipEndLen-2]) | int(b[i+previewZipEndLen-1])<<8
			if n+previewZipEndLen+i > len(b) {
				return -1
			}
			return i
		}
	}
	return -1
}

// previewZipHeader reads one central directory header: the name, the sizes (with the
// zip64 extra field) and what FileInfo needs to tell a folder.
func previewZipHeader(r io.Reader) (zip.FileHeader, error) {
	var h zip.FileHeader
	var fixed [46]byte
	if _, err := io.ReadFull(r, fixed[:]); err != nil {
		return h, err
	}
	le := binary.LittleEndian
	if le.Uint32(fixed[:]) != 0x02014b50 {
		return h, zip.ErrFormat
	}
	h.CreatorVersion = le.Uint16(fixed[4:])
	size32, packed32 := le.Uint32(fixed[24:]), le.Uint32(fixed[20:])
	h.UncompressedSize64, h.CompressedSize64 = uint64(size32), uint64(packed32)
	nameLen, extraLen, commentLen := int(le.Uint16(fixed[28:])), int(le.Uint16(fixed[30:])), int(le.Uint16(fixed[32:]))
	h.ExternalAttrs = le.Uint32(fixed[38:])
	offset := le.Uint32(fixed[42:])
	d := make([]byte, nameLen+extraLen+commentLen)
	if _, err := io.ReadFull(r, d); err != nil {
		return h, err
	}
	h.Name = string(d[:nameLen])
	extra := d[nameLen : nameLen+extraLen]
	for len(extra) >= 4 {
		tag, n := le.Uint16(extra), int(le.Uint16(extra[2:]))
		extra = extra[4:]
		if len(extra) < n {
			break
		}
		field := extra[:n]
		extra = extra[n:]
		if tag != 1 {
			continue
		}
		// The zip64 field holds, in order, only the values the header maxed out.
		for i, maxed := range []bool{size32 == ^uint32(0), packed32 == ^uint32(0), offset == ^uint32(0)} {
			if !maxed {
				continue
			}
			if len(field) < 8 {
				return h, zip.ErrFormat
			}
			if i == 0 {
				h.UncompressedSize64 = le.Uint64(field)
			}
			field = field[8:]
		}
	}
	return h, nil
}

// previewFormatJSON lays out a JSON file for reading. It reports false for a file
// that is too large or is not JSON, which then shows as it is.
func previewFormatJSON(f *os.File, size int64, out map[string]any) bool {
	if size > previewJSONMax {
		return false
	}
	b, err := readAllOf(f, previewJSONMax)
	if err != nil {
		return false
	}
	if previewJSONIndentSize(b) > previewJSONOutMax {
		return false
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, b, "", "  ") != nil {
		return false
	}
	out["kind"] = "text"
	out["text"] = pretty.String()
	out["truncated"] = false
	out["formatted"] = true
	return true
}

// previewJSONIndentSize estimates the size of what json.Indent writes for b, counted in one
// pass without laying anything out: each line it starts costs a newline and two spaces a
// level. It is an estimate to hold against the bound, not a bound and not exact: it counts the
// input's own whitespace, and it does not count the space json.Indent writes after each ':'.
func previewJSONIndentSize(b []byte) int64 {
	total, depth := int64(len(b)), int64(0)
	inString, escaped, opened := false, false, false
	line := func() { total += 1 + 2*depth }
	for _, c := range b {
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch c {
		case ' ', '\t', '\r', '\n':
			continue
		case '{', '[':
			if opened {
				line()
			}
			depth++
			opened = true
			continue
		case '}', ']':
			depth = max(depth-1, 0)
			if !opened {
				line()
			}
		case ',':
			line()
		default:
			if opened {
				line()
			}
			inString = c == '"'
		}
		opened = false
		if total > previewJSONOutMax {
			return total
		}
	}
	return total
}

// previewDetails adds what a person asks about a file: its permissions and owner,
// and what git says of it. A file git sees as changed gets its diff, and a PDF the
// words of the page.
func previewDetails(ctx context.Context, f *os.File, fi os.FileInfo, path string, want PreviewWant, out map[string]any) {
	out["details"] = true
	out["mode"] = fi.Mode().Perm().String()[1:]
	out["owner"], out["group"] = previewFileOwner(fi)
	dir, name := filepath.Dir(path), filepath.Base(path)
	if strings.EqualFold(filepath.Ext(path), ".pdf") {
		page := strconv.Itoa(max(want.Page, 1))
		if b, err := previewTool(ctx, f, "pdftotext", "-f", page, "-l", page, "-layout", fdArg, "-"); err == nil {
			out["words"] = strings.ToValidUTF8(string(b[:min(len(b), previewWordsMax)]), "")
		}
	}
	if _, _, root, ok := gitstate.Locate(dir); !ok || root == "" {
		return
	}
	// core.fsmonitor names a program to run, and a folder can set it.
	safe := []string{"-c", "core.fsmonitor=false", "-c", "core.quotePath=false"}
	st, err := review.Git(ctx, dir, nil, append(safe, "status", "--porcelain=v1", "--", name)...)
	if err != nil {
		return
	}
	branch := gitstate.Branch(dir)
	out["git"] = previewGitWords(st, branch)
	if st == "" || strings.HasPrefix(st, "??") {
		return
	}
	diff, err := review.GitHead(ctx, dir, nil, previewDiffMax, append(safe, "diff", "--no-color", "--no-ext-diff", "--no-textconv", "HEAD", "--", name)...)
	if err == nil {
		out["diff"] = strings.ToValidUTF8(diff, "")
	}
}

// previewGitWords is a line of git status in words.
func previewGitWords(status, branch string) string {
	on := ""
	if branch != "" {
		on = " on " + branch
	}
	switch {
	case status == "":
		return "no changes" + on
	case strings.HasPrefix(status, "??"):
		return "not in git" + on
	case strings.HasPrefix(status, "!!"):
		return "ignored"
	case len(status) > 1 && status[1] == ' ':
		return "changed" + on + ", staged"
	}
	return "changed" + on + ", not staged"
}

// previewGIFCut walks a GIF's blocks with nothing decoded. It returns how many frames
// the file holds, up to limit, and the file cut after the last of them. A small GIF
// can hold thousands of full frames, so the decoder must not see them all.
func previewGIFCut(b []byte, limit int) (int, []byte) {
	if len(b) < 13 {
		return 0, nil
	}
	i := 13
	if b[10]&0x80 != 0 {
		i += 3 << ((b[10] & 7) + 1)
	}
	skipBlocks := func(i int) int {
		for i < len(b) && b[i] != 0 {
			i += int(b[i]) + 1
		}
		return i + 1
	}
	frames := 0
	for i < len(b) {
		switch b[i] {
		case 0x21:
			i = skipBlocks(i + 2)
		case 0x2c:
			if i+10 > len(b) {
				return frames, nil
			}
			flags := b[i+9]
			i += 10
			if flags&0x80 != 0 {
				i += 3 << ((flags & 7) + 1)
			}
			i = skipBlocks(i + 1)
			frames++
			if frames == limit && i < len(b) {
				return frames, append(b[:i:i], 0x3b)
			}
		case 0x3b:
			return frames, b
		default:
			return frames, nil
		}
	}
	return frames, nil
}

// previewGIFFrame decodes one frame of a GIF as it looks when played, and adds how
// many frames there are to out. A file that is not safe to decode whole gives nil, and
// the caller shows the first frame.
func previewGIFFrame(f *os.File, size int64, cfg image.Config, page int, out map[string]any) image.Image {
	if size > previewImageMax {
		return nil
	}
	b, err := readAllOf(f, previewImageMax)
	if err != nil {
		return nil
	}
	frames, cut := previewGIFCut(b, previewGIFFrames)
	if frames < 2 || cut == nil || int64(cfg.Width)*int64(cfg.Height)*int64(frames+previewGIFCanvases) > previewDecodeMax {
		return nil
	}
	all, err := gif.DecodeAll(bytes.NewReader(cut))
	if err != nil || len(all.Image) == 0 {
		return nil
	}
	n := min(max(page, 1), len(all.Image))
	canvas := image.NewRGBA(image.Rect(0, 0, cfg.Width, cfg.Height))
	for i := range n {
		frame := all.Image[i]
		before := []uint8(nil)
		if all.Disposal[i] == gif.DisposalPrevious && i < n-1 {
			before = bytes.Clone(canvas.Pix)
		}
		draw.Draw(canvas, frame.Bounds(), frame, frame.Bounds().Min, draw.Over)
		if i == n-1 {
			break
		}
		switch all.Disposal[i] {
		case gif.DisposalBackground:
			draw.Draw(canvas, frame.Bounds(), image.NewUniform(color.Transparent), image.Point{}, draw.Src)
		case gif.DisposalPrevious:
			copy(canvas.Pix, before)
		}
	}
	out["pages"], out["page"] = len(all.Image), n
	return canvas
}

// previewHead adds the start of a binary file to out, for a hex view.
func previewHead(f *os.File, size int64, out map[string]any) {
	head := make([]byte, min(size, previewHexMax))
	n, _ := f.ReadAt(head, 0)
	if n > 0 {
		out["head"] = base64.StdEncoding.EncodeToString(head[:n])
	}
}
