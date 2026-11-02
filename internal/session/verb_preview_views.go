package session

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
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

	"github.com/Gaurav-Gosain/tuios/internal/gitstate"
	"github.com/Gaurav-Gosain/tuios/internal/review"
)

const (
	// previewJSONMax is the largest JSON file that is laid out for reading.
	previewJSONMax = 1 << 20
	// previewHexMax is the start of a binary file a preview sends.
	previewHexMax = 16 << 10
	// previewArchiveItems and previewArchiveBytes bound a listing. A small gzip
	// can unpack to a great deal, and tar has to read through all of it.
	previewArchiveItems = 2000
	previewArchiveBytes = 1 << 30
	previewArchiveTime  = 3 * time.Second
	// previewDiffMax is the most of a diff a preview sends.
	previewDiffMax = 256 << 10
	// previewGIFFrames is the most frames of a GIF that can be stepped through.
	previewGIFFrames = 64
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
		entries = append(entries, PreviewArchiveEntry{Name: h.Name, Size: h.Size, Dir: h.Typeflag == tar.TypeDir})
	}
	return entries, true, nil
}

// previewZip lists a zip file from its central directory, with nothing unpacked.
func previewZip(f *os.File, size int64) ([]PreviewArchiveEntry, bool, error) {
	z, err := zip.NewReader(f, size)
	if err != nil {
		return nil, false, err
	}
	capped := len(z.File) > previewArchiveItems
	var entries []PreviewArchiveEntry
	for _, e := range z.File[:min(len(z.File), previewArchiveItems)] {
		entries = append(entries, PreviewArchiveEntry{Name: e.Name, Size: int64(e.UncompressedSize64), Dir: e.FileInfo().IsDir()})
	}
	return entries, capped, nil
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
	diff, err := review.Git(ctx, dir, nil, append(safe, "diff", "--no-color", "--no-ext-diff", "--no-textconv", "HEAD", "--", name)...)
	if err == nil {
		if len(diff) > previewDiffMax {
			diff = diff[:previewDiffMax]
		}
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
	if frames < 2 || cut == nil || int64(cfg.Width)*int64(cfg.Height)*int64(frames) > previewDecodeMax {
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
