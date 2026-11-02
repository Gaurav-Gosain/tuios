package session

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Gaurav-Gosain/tuios/internal/childproc"
	"tuios.dev/shot"
)

// file-preview: what Quick Look shows, made on the machine the file is on.
//
// A preview of a file on another machine should not cost the file: a 40 MB
// photo is shown at 1600 pixels, a PDF as its first page, a film as one frame.
// So the machine with the file does the heavy part and sends the small result,
// and the client decodes what comes back (tuios-gpui's tuios-preview crate). A
// text file is sent as its first 256 KiB; the client highlights it.
//
// PDF pages and film frames come from pdftoppm and ffmpeg when that machine
// has them. Without pdftoppm a PDF up to 8 MiB is sent as it is, for a client
// that can draw one. Without ffmpeg a film is described, not shown.
//
// A preview reads a file's bytes, so it is held to the rules file-read keeps:
// the scope table denies it to a pane, the link policy asks for the files
// capability, and a call over a link goes through the read deny list and
// files_roots (checkLinkRead). The file is opened once, through
// openLinkRead, and everything after reads that open file: the decoders read
// it, and pdftoppm, pdfinfo, ffprobe and ffmpeg get it as /dev/fd/3, not by
// its name. A name checked and then handed to a tool would race a rename that
// swaps a key in under it.

const (
	previewMaxPx      = 1600
	previewMaxPxLimit = 4096
	previewTextMax    = 256 << 10
	previewWindowMax  = 1 << 20
	previewSeekChunk  = 1 << 20
	previewLineMax    = 1 << 20
	// previewSeekBudget bounds one search or one count. The answer says how far it
	// read, and the next call goes on from there.
	previewSeekBudget  = 5 * time.Second
	previewImageMax    = 64 << 20
	previewImageDimMax = 10000
	previewPDFBytesMax = 8 << 20
	previewWordsMax    = 256 << 10
	previewSVGMax      = 4 << 20
	previewToolTimeout = 20 * time.Second
	// previewDecodeMax bounds the memory one image's decode and scale may
	// take, the decoder's own buffers counted (decodeCost). A small file can
	// hold a huge picture: a 400 KB PNG of 10000 x 10000 pixels decodes to
	// 400 MB, and an 8 MB progressive JPEG of 9000 x 7000 holds 945 MB of
	// coefficients and planes while it decodes.
	previewDecodeMax = 256 << 20
	// previewPassMax is the largest picture sent as it is, for the client
	// to decode: a WebP or a BMP, which this machine does not decode, and a
	// picture too large to decode here within previewDecodeMax. The GUI
	// decodes them with its own caps (tuios-gpui's tuios-preview crate).
	previewPassMax = 16 << 20
)

// previewDecodeSlot lets one image decode at a time, so previews asked for
// together cannot add their pictures up in the daemon's memory.
var previewDecodeSlot = make(chan struct{}, 1)

var (
	previewImageExt = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".bmp": true}
	previewVideoExt = map[string]bool{".mp4": true, ".mkv": true, ".webm": true, ".mov": true, ".avi": true, ".m4v": true}
)

// verbFilePreview makes the preview of one file on this machine.
func (d *Daemon) verbFilePreview(cs *connState, params json.RawMessage) (any, *verbError) {
	var p struct {
		Path       string `json:"path"`
		MaxPx      int    `json:"max_px"`
		Page       int    `json:"page"`
		PagesKnown bool   `json:"pages_known"`
		Words      bool   `json:"words"`
		Offset     *int64 `json:"offset"`
		Limit      int    `json:"limit"`
		Find       string `json:"find"`
		From       int64  `json:"from"`
		Back       bool   `json:"back"`
		Line       int64  `json:"line"`
	}
	if verr := decodeParams(params, &p); verr != nil {
		return nil, verr
	}
	path, verr := expandPath(p.Path)
	if verr != nil {
		return nil, verr
	}
	maxPx := p.MaxPx
	if maxPx <= 0 {
		maxPx = previewMaxPx
	}
	maxPx = min(maxPx, previewMaxPxLimit)
	page := max(p.Page, 1)

	// The name is judged before anything about the file is said, so a
	// folder or a pipe in the deny list gets the same refusal as a file.
	if verr := d.checkLinkRead(cs, path); verr != nil {
		return nil, verr
	}
	f, fi, verr, err := d.openLinkRead(cs, path)
	if verr != nil {
		return nil, verr
	}
	if err != nil && fi == nil {
		return nil, fileError("preview", path, err)
	}
	ctx, cancel := context.WithTimeout(d.ctx, previewToolTimeout)
	defer cancel()
	if (p.Find != "" || p.Line > 0) && err == nil {
		defer func() { _ = f.Close() }()
		out := map[string]any{"path": path, "name": filepath.Base(path), "size": fi.Size(), "mtime": fi.ModTime().UnixMilli()}
		ctx, cancel := context.WithTimeout(d.ctx, previewSeekBudget)
		defer cancel()
		previewSeek(ctx, f, fi.Size(), PreviewSeek{Find: p.Find, From: p.From, Back: p.Back, Line: p.Line}, out)
		return out, nil
	}
	if p.Offset != nil && err == nil {
		defer func() { _ = f.Close() }()
		out := map[string]any{"path": path, "name": filepath.Base(path), "size": fi.Size(), "mtime": fi.ModTime().UnixMilli()}
		previewWindow(f, fi.Size(), *p.Offset, p.Limit, out)
		return out, nil
	}
	return previewOpened(ctx, f, fi, err, path, maxPx, PreviewPDF{Page: page, PagesKnown: p.PagesKnown, Words: p.Words}), nil
}

// PreviewLocalFile makes the preview of a file on this machine, in this
// process, as file-preview makes it in the daemon: the same kinds, caps and
// tools. The rail's quick look calls it for a file on the client's own
// machine when no daemon serves the client. path is absolute.
func PreviewLocalFile(ctx context.Context, path string, maxPx int) (map[string]any, error) {
	if maxPx <= 0 {
		maxPx = previewMaxPx
	}
	maxPx = min(maxPx, previewMaxPxLimit)
	f, fi, err := openRegular(path)
	if err != nil && fi == nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, previewToolTimeout)
	defer cancel()
	return previewOpened(ctx, f, fi, err, path, maxPx, PreviewPDF{Page: 1}), nil
}

// PreviewPDF is what a preview asks of a PDF: a page, and how. PagesKnown leaves
// out the count, which takes a run of pdfinfo. Words sends the page's words in place
// of its picture.
type PreviewPDF struct {
	MaxPx      int
	Page       int
	PagesKnown bool
	Words      bool
}

// PreviewLocalPDF is PreviewLocalFile for one page of a PDF. A cancelled ctx kills
// the tool that is running.
func PreviewLocalPDF(ctx context.Context, path string, want PreviewPDF) (map[string]any, error) {
	maxPx := want.MaxPx
	if maxPx <= 0 {
		maxPx = previewMaxPx
	}
	maxPx = min(maxPx, previewMaxPxLimit)
	f, fi, err := openRegular(path)
	if err != nil && fi == nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, previewToolTimeout)
	defer cancel()
	return previewOpened(ctx, f, fi, err, path, maxPx, want), nil
}

// PreviewLocalWindow is a window of a text file on this machine, as file-preview
// sends one for offset and limit.
func PreviewLocalWindow(path string, offset int64, limit int) (map[string]any, error) {
	f, fi, err := openRegular(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	out := map[string]any{"path": path, "name": filepath.Base(path), "size": fi.Size(), "mtime": fi.ModTime().UnixMilli()}
	previewWindow(f, fi.Size(), offset, limit, out)
	return out, nil
}

// PreviewSeek says what to look for in a text: Find from byte From, going back
// when Back is set, or line number Line.
type PreviewSeek struct {
	Find string
	From int64
	Back bool
	Line int64
}

// PreviewLocalSeek looks in a text on this machine, as file-preview does for
// find and line.
func PreviewLocalSeek(ctx context.Context, path string, seek PreviewSeek) (map[string]any, error) {
	f, fi, err := openRegular(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	ctx, cancel := context.WithTimeout(ctx, previewSeekBudget)
	defer cancel()
	out := map[string]any{"path": path, "name": filepath.Base(path), "size": fi.Size(), "mtime": fi.ModTime().UnixMilli()}
	previewSeek(ctx, f, fi.Size(), seek, out)
	return out, nil
}

// PreviewShowsPicture reports whether a file of this name previews as a picture:
// an image, a PDF page or a film frame.
func PreviewShowsPicture(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return previewImageExt[ext] || previewVideoExt[ext] || ext == ".pdf"
}

// previewOpened is the preview of the file f, opened through openRegular.
// openErr is openRegular's refusal of a file that is not regular, whose
// fi then says what it is; f is nil then. It closes f.
func previewOpened(ctx context.Context, f *os.File, fi fs.FileInfo, openErr error, path string, maxPx int, pdf PreviewPDF) map[string]any {
	out := map[string]any{
		"path":  path,
		"name":  filepath.Base(path),
		"size":  fi.Size(),
		"mtime": fi.ModTime().UnixMilli(),
	}
	if openErr != nil {
		// openRegular refused it and said what it is, without reading it.
		// A named pipe would hold the preview until something wrote to it,
		// and a device has no end. Neither is opened past that.
		if fi.IsDir() {
			out["kind"] = "dir"
			return out
		}
		out["kind"] = "binary"
		out["note"] = "This is a pipe, a device or a socket. It has no preview."
		return out
	}
	defer func() { _ = f.Close() }()
	ext := strings.ToLower(filepath.Ext(path))

	switch {
	case previewImageExt[ext]:
		if err := previewImage(ctx, f, ext, fi.Size(), maxPx, out); err != nil {
			describeBinary(f, out, err.Error())
		}
		return out
	case ext == ".svg":
		if fi.Size() <= previewSVGMax {
			if b, err := readAllOf(f, previewSVGMax); err == nil {
				out["kind"] = "svg"
				out["text"] = string(b)
				return out
			}
		}
		describeBinary(f, out, "The drawing is too large to show.")
		return out
	case ext == ".pdf":
		previewPDF(ctx, f, fi.Size(), maxPx, pdf, out)
		return out
	case previewVideoExt[ext]:
		previewVideo(ctx, f, maxPx, out)
		return out
	}
	if previewText(f, fi.Size(), out) {
		return out
	}
	describeBinary(f, out, "")
	return out
}

// readAllOf reads at most limit bytes of f from its start.
func readAllOf(f *os.File, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.NewSectionReader(f, 0, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, errors.New("the file grew past the limit")
	}
	return b, nil
}

// previewToolsReadFD says a tool can be handed the open file as /dev/fd/3.
// Windows has no such name, so there a PDF is sent as it is and a film is
// described.
var previewToolsReadFD = runtime.GOOS != "windows" && runtime.GOOS != "js"

// previewTool runs a tool over the open file f, which it reads as
// /dev/fd/3. args name the file as fdArg. It returns the tool's stdout.
func previewTool(ctx context.Context, f *os.File, tool string, args ...string) ([]byte, error) {
	if !previewToolsReadFD {
		return nil, errors.New("no tools on this system")
	}
	bin, err := exec.LookPath(tool)
	if err != nil {
		return nil, err
	}
	cmd := childproc.CommandContext(ctx, bin, args...)
	cmd.ExtraFiles = []*os.File{f}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return stdout.Bytes(), nil
}

// previewProtocols and previewFormats hold ffmpeg and ffprobe to the open
// file and to the film containers a preview is made of. A file named .mp4
// can hold a playlist (HLS) or a concat list that names other files, and an
// ffmpeg that followed it would read them; the demuxers that do are not on
// the list.
const (
	previewProtocols = "file"
	previewFormats   = "mov,mp4,m4a,3gp,3g2,mj2,matroska,webm,avi"
)

// fdArg is how a preview tool names the file it was handed.
const fdArg = "/dev/fd/3"

// decodedBytesPerPixel is what one pixel of a decoded image takes in memory,
// at the most, for the color model the image's header names.
func decodedBytesPerPixel(m color.Model) int64 {
	switch m {
	case color.RGBA64Model, color.NRGBA64Model:
		return 8
	case color.GrayModel, color.AlphaModel:
		return 1
	case color.Gray16Model, color.Alpha16Model:
		return 2
	case color.YCbCrModel, color.NYCbCrAModel:
		return 4
	}
	if _, ok := m.(color.Palette); ok {
		return 1
	}
	return 4
}

// previewNote is a sentence a preview without a picture shows the person, in
// its note. It is an error so the image path can hand it back.
type previewNote string

func (n previewNote) Error() string { return string(n) }

const (
	noteTooLarge   previewNote = "The image is too large to show."
	noteUnreadable previewNote = "The image could not be read."
	noteTooSlow    previewNote = "The preview took too long. Try again."
)

// previewPassExt are the pictures sent as they are, never decoded here.
var previewPassExt = map[string]string{".webp": "image/webp", ".bmp": "image/bmp"}

// previewImage sends a picture at most maxPx on its long side, decoded and
// scaled here when that fits in previewDecodeMax, and otherwise as it is for
// the client to decode, when it is at most previewPassMax.
func previewImage(ctx context.Context, f *os.File, ext string, size int64, maxPx int, out map[string]any) error {
	if mime, ok := previewPassExt[ext]; ok {
		return passImage(f, size, mime, out)
	}
	if size > previewImageMax {
		return noteTooLarge
	}
	cfg, format, err := image.DecodeConfig(io.NewSectionReader(f, 0, size))
	if err != nil {
		return noteUnreadable
	}
	if cfg.Width > previewImageDimMax || cfg.Height > previewImageDimMax {
		return noteTooLarge
	}
	if decodeCost(f, size, format, cfg, maxPx) > previewDecodeMax {
		out["width"] = cfg.Width
		out["height"] = cfg.Height
		return passImage(f, size, "image/"+format, out)
	}
	select {
	case previewDecodeSlot <- struct{}{}:
		defer func() { <-previewDecodeSlot }()
	case <-ctx.Done():
		return noteTooSlow
	}
	var img image.Image
	r := io.NewSectionReader(f, 0, size)
	if format == "gif" {
		// The first frame. An animation is not played in a preview.
		img, err = gif.Decode(r)
	} else {
		img, _, err = image.Decode(r)
	}
	if err != nil {
		return noteUnreadable
	}
	scaled := fit(img, maxPx)
	data, mime, err := encodePreview(scaled, format == "jpeg")
	if err != nil {
		return err
	}
	b := scaled.Bounds()
	out["kind"] = "image"
	out["mime"] = mime
	out["width"] = cfg.Width
	out["height"] = cfg.Height
	out["image_width"] = b.Dx()
	out["image_height"] = b.Dy()
	out["image"] = base64.StdEncoding.EncodeToString(data)
	return nil
}

// passImage sends the picture's own bytes, for the client to decode.
func passImage(f *os.File, size int64, mime string, out map[string]any) error {
	if size > previewPassMax {
		return noteTooLarge
	}
	b, err := readAllOf(f, previewPassMax)
	if err != nil {
		return noteUnreadable
	}
	out["kind"] = "image"
	out["mime"] = mime
	out["image"] = base64.StdEncoding.EncodeToString(b)
	return nil
}

// decodeCost is the most memory decoding the picture and scaling it to
// maxPx takes: the decoded image, the decoder's own buffers, and the scaled
// copy. A progressive JPEG keeps every block's coefficients, as int32, until
// its last scan: four bytes a pixel for each component, on top of the planes.
func decodeCost(f *os.File, size int64, format string, cfg image.Config, maxPx int) int64 {
	px := int64(cfg.Width) * int64(cfg.Height)
	cost := px * decodedBytesPerPixel(cfg.ColorModel)
	if format == "png" {
		// An interlaced PNG decodes each of its seven passes into an image
		// of its own before it puts them together.
		var ihdr [1]byte
		if _, err := f.ReadAt(ihdr[:], 28); err == nil && ihdr[0] == 1 {
			cost *= 2
		}
	}
	if format == "jpeg" {
		progressive, comps := jpegFrame(io.NewSectionReader(f, 0, size))
		// The planes: one byte a pixel for each component at the most (no
		// chroma subsampling).
		cost = px * int64(comps)
		if progressive {
			cost += px * int64(comps) * 4
		}
	}
	// The scaled copy, RGBA, at most maxPx on its long side.
	sw, sh := int64(min(cfg.Width, maxPx)), int64(min(cfg.Height, maxPx))
	return cost + sw*sh*4
}

// jpegFrame reads a JPEG's frame header: whether it is progressive, and how
// many components it has (3 when the header is not found in the first MiB,
// which costs the most a baseline file can).
func jpegFrame(r io.ReaderAt) (progressive bool, comps int) {
	const limit = 1 << 20
	comps = 3
	var hdr [4]byte
	off := int64(2) // after SOI
	for off < limit {
		if _, err := r.ReadAt(hdr[:2], off); err != nil || hdr[0] != 0xff {
			return true, comps // unknown: count it as the dearer kind
		}
		marker := hdr[1]
		if marker == 0xff {
			off++ // fill byte
			continue
		}
		off += 2
		if marker == 0x01 || (marker >= 0xd0 && marker <= 0xd9) {
			continue // no length
		}
		if _, err := r.ReadAt(hdr[2:4], off); err != nil {
			return true, comps
		}
		n := int64(hdr[2])<<8 | int64(hdr[3])
		if marker >= 0xc0 && marker <= 0xcf && marker != 0xc4 && marker != 0xc8 && marker != 0xcc {
			var sof [6]byte
			if _, err := r.ReadAt(sof[:], off+2); err != nil {
				return true, comps
			}
			if c := int(sof[5]); c >= 1 && c <= 4 {
				comps = c
			}
			switch marker {
			case 0xc2, 0xc6, 0xca, 0xce:
				return true, comps
			}
			return false, comps
		}
		off += n
	}
	return true, comps
}

// fit scales img down to at most maxPx on its long side. A bilinear scale
// samples the source in place; CatmullRom built a float buffer of the
// destination's width by the source's height, 358 MB for a 9000 x 7000
// picture at 1600 px.
func fit(img image.Image, maxPx int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= maxPx && h <= maxPx {
		return img
	}
	if w >= h {
		h = max(h*maxPx/w, 1)
		w = maxPx
	} else {
		w = max(w*maxPx/h, 1)
		h = maxPx
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	shot.ScaleBilinear(dst, dst.Bounds(), img, b, false)
	return dst
}

// encodePreview is the image as JPEG for a photo and PNG for everything else,
// which may hold transparency or hard edges.
func encodePreview(img image.Image, photo bool) ([]byte, string, error) {
	var buf bytes.Buffer
	if photo {
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 88}); err != nil {
			return nil, "", err
		}
		return buf.Bytes(), "image/jpeg", nil
	}
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), "image/png", nil
}

// previewPDF sends one page as an image, or the file for a client to draw.
func previewPDF(ctx context.Context, f *os.File, size int64, maxPx int, want PreviewPDF, out map[string]any) {
	out["kind"] = "pdf"
	page := max(want.Page, 1)
	if !want.PagesKnown {
		if pages := pdfPages(ctx, f); pages > 0 {
			out["pages"] = pages
		}
	}
	out["page"] = page
	if want.Words {
		b, err := previewTool(ctx, f, "pdftotext", "-f", strconv.Itoa(page), "-l", strconv.Itoa(page), "-layout", fdArg, "-")
		switch {
		case err == nil:
			out["words"] = strings.ToValidUTF8(string(b[:min(len(b), previewWordsMax)]), "")
		case previewToolMissing("pdftotext"):
			out["no_tool"] = "pdftotext"
		default:
			out["note"] = "tuios could not read this page."
		}
		return
	}
	if png1, err := previewTool(ctx, f, "pdftoppm", "-png", "-singlefile", "-f", strconv.Itoa(page), "-l", strconv.Itoa(page),
		"-scale-to", strconv.Itoa(maxPx), fdArg); err == nil && len(png1) > 0 {
		if cfg, err := png.DecodeConfig(bytes.NewReader(png1)); err == nil {
			out["image_width"] = cfg.Width
			out["image_height"] = cfg.Height
		}
		out["mime"] = "image/png"
		out["image"] = base64.StdEncoding.EncodeToString(png1)
		return
	}
	if previewToolMissing("pdftoppm") {
		out["no_tool"] = "pdftoppm"
	} else if page > 1 || want.PagesKnown {
		// The file has been drawn before, so this page is what failed.
		out["note"] = "tuios could not draw this page."
		return
	}
	if size <= previewPDFBytesMax {
		if b, err := readAllOf(f, previewPDFBytesMax); err == nil {
			out["pdf"] = base64.StdEncoding.EncodeToString(b)
			return
		}
	}
	out["note"] = "This machine has no pdftoppm, and the file is too large to send for a preview."
}

// previewToolMissing reports whether a tool that could run on this system is not
// installed.
func previewToolMissing(tool string) bool {
	if !previewToolsReadFD {
		return false
	}
	_, err := exec.LookPath(tool)
	return err != nil
}

// pdfPages counts a PDF's pages with pdfinfo, 0 when it cannot.
func pdfPages(ctx context.Context, f *os.File) int {
	b, err := previewTool(ctx, f, "pdfinfo", fdArg)
	if err != nil {
		return 0
	}
	for line := range strings.SplitSeq(string(b), "\n") {
		if rest, ok := strings.CutPrefix(line, "Pages:"); ok {
			n, _ := strconv.Atoi(strings.TrimSpace(rest))
			return n
		}
	}
	return 0
}

// previewVideo sends one frame from 5 % into the film, the first key frame
// there, as yazi does.
func previewVideo(ctx context.Context, f *os.File, maxPx int, out map[string]any) {
	out["kind"] = "video"
	durMs := videoDuration(ctx, f)
	if durMs > 0 {
		out["duration_ms"] = durMs
	}
	if _, err := exec.LookPath("ffmpeg"); err != nil || !previewToolsReadFD {
		out["note"] = "This machine has no ffmpeg, so no frame can be shown."
		return
	}
	at := float64(durMs) * 0.05 / 1000
	args := []string{"-v", "error", "-nostdin", "-protocol_whitelist", previewProtocols, "-format_whitelist", previewFormats}
	if at > 0 {
		args = append(args, "-ss", strconv.FormatFloat(at, 'f', 3, 64))
	}
	scale := "scale='min(" + strconv.Itoa(maxPx) + ",iw)':'min(" + strconv.Itoa(maxPx) + ",ih)':force_original_aspect_ratio=decrease"
	args = append(args, "-skip_frame", "nokey", "-i", fdArg, "-frames:v", "1", "-vf", scale, "-f", "image2pipe", "-vcodec", "mjpeg", "-q:v", "3", "-")
	frame, err := previewTool(ctx, f, "ffmpeg", args...)
	if err != nil || len(frame) == 0 {
		out["note"] = "No frame could be read from the film."
		return
	}
	if cfg, err := jpeg.DecodeConfig(bytes.NewReader(frame)); err == nil {
		out["image_width"] = cfg.Width
		out["image_height"] = cfg.Height
	}
	out["mime"] = "image/jpeg"
	out["image"] = base64.StdEncoding.EncodeToString(frame)
}

// videoDuration is the film's length in ms from ffprobe, 0 when it cannot.
func videoDuration(ctx context.Context, f *os.File) int64 {
	b, err := previewTool(ctx, f, "ffprobe", "-v", "error", "-protocol_whitelist", previewProtocols, "-format_whitelist", previewFormats, "-show_entries", "format=duration", "-of", "default=nw=1:nk=1", fdArg)
	if err != nil {
		return 0
	}
	sec, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
	if err != nil {
		return 0
	}
	return int64(sec * 1000)
}

// previewText sends the start of a file that reads as text. It reports false
// for a file that does not.
func previewText(f *os.File, size int64, out map[string]any) bool {
	buf := make([]byte, min(size, previewTextMax))
	n, _ := io.ReadFull(io.NewSectionReader(f, 0, int64(len(buf))), buf)
	buf = buf[:n]
	head := buf[:min(len(buf), 8192)]
	if bytes.IndexByte(head, 0) >= 0 {
		return false
	}
	// A cut through the last character is not a reason to call it binary.
	trimmed := buf
	for i := 0; i < 4 && len(trimmed) > 0 && !utf8.Valid(trimmed); i++ {
		trimmed = trimmed[:len(trimmed)-1]
	}
	if !utf8.Valid(trimmed) {
		return false
	}
	out["kind"] = "text"
	out["text"] = string(trimmed)
	out["truncated"] = size > int64(len(trimmed))
	out["mime"] = http.DetectContentType(head)
	return true
}

// previewWindow sends a window of a text file: at most limit bytes from offset.
// The window holds whole lines. It starts at the first line that starts at or
// after offset, and it ends after the last line that ends inside it. A line
// longer than the window is sent cut. next_offset is where the next window
// starts, and a window that ends at offset N asked for from N-limit ends exactly
// there, so a client can read backward too.
//
// No line is counted and nothing is kept: the cost is one read of limit bytes,
// whatever the size of the file.
func previewWindow(f *os.File, size, offset int64, limit int, out map[string]any) {
	if limit <= 0 {
		limit = previewTextMax
	}
	limit = min(limit, previewWindowMax)
	offset = min(max(offset, 0), size)
	buf := make([]byte, min(int64(limit), size-offset))
	n, _ := f.ReadAt(buf, offset)
	buf = buf[:n]
	start, end := 0, len(buf)
	if offset > 0 {
		var prev [1]byte
		if _, err := f.ReadAt(prev[:], offset-1); err == nil && prev[0] != '\n' {
			if i := bytes.IndexByte(buf, '\n'); i >= 0 {
				start = i + 1
			}
		}
	}
	if offset+int64(end) < size {
		if i := bytes.LastIndexByte(buf[start:], '\n'); i >= 0 {
			end = start + i + 1
		}
	}
	out["kind"] = "text"
	out["text"] = strings.ToValidUTF8(string(buf[start:end]), "\uFFFD")
	out["offset"] = offset + int64(start)
	out["next_offset"] = offset + int64(end)
	out["truncated"] = offset+int64(end) < size
}

// previewSeek looks in a text one chunk at a time, for at most previewSeekBudget.
// It holds one chunk, whatever the size of the file. offset is the start of the
// line it found, or -1. reached is how far it read, so a later call can go on
// from there, and counted is the lines it passed on the way to a line number.
func previewSeek(ctx context.Context, f *os.File, size int64, seek PreviewSeek, out map[string]any) {
	out["kind"] = "text"
	out["offset"] = int64(-1)
	buf := make([]byte, previewSeekChunk)
	if seek.Line > 0 {
		previewSeekLine(ctx, f, size, seek.Line, buf, out)
		return
	}
	needle := []byte(seek.Find)
	// A search in lower case ignores case, and one with a capital does not.
	fold := !bytes.ContainsFunc(needle, unicode.IsUpper)
	over := len(needle) - 1
	at := min(max(seek.From, 0), size)
	for ctx.Err() == nil {
		start := at
		if seek.Back {
			start = max(at-int64(len(buf)), 0)
		}
		n, _ := f.ReadAt(buf[:min(int64(len(buf)), max(size-start, 0))], start)
		chunk := buf[:n]
		if seek.Back {
			chunk = chunk[:min(int64(n), at-start)]
		}
		hay := chunk
		if fold {
			hay = bytes.ToLower(chunk)
		}
		i := bytes.Index(hay, needle)
		if seek.Back {
			i = bytes.LastIndex(hay, needle)
		}
		if i >= 0 {
			out["offset"] = previewLineStart(f, start+int64(i))
			out["reached"] = start + int64(i)
			return
		}
		if seek.Back {
			if start == 0 {
				at = 0
				break
			}
			at = min(start+int64(over), at-1)
			continue
		}
		if start+int64(n) >= size || n == 0 {
			at = size
			break
		}
		at = start + int64(n) - int64(over)
		if at <= start {
			at = start + int64(n)
		}
	}
	out["reached"] = at
}

// previewSeekLine finds the start of line number line.
func previewSeekLine(ctx context.Context, f *os.File, size, line int64, buf []byte, out map[string]any) {
	if line <= 1 {
		out["offset"] = int64(0)
		out["counted"] = int64(1)
		out["reached"] = int64(0)
		return
	}
	var at, counted int64
	for ctx.Err() == nil && at < size {
		n, _ := f.ReadAt(buf, at)
		if n == 0 {
			break
		}
		chunk := buf[:n]
		for {
			i := bytes.IndexByte(chunk, '\n')
			if i < 0 {
				break
			}
			counted++
			chunk = chunk[i+1:]
			if counted == line-1 {
				start := at + int64(n-len(chunk))
				if start >= size {
					// The newline ends the file: that line is not there.
					break
				}
				out["offset"] = start
				out["counted"] = line
				out["reached"] = start
				return
			}
		}
		at += int64(n)
	}
	out["counted"] = counted
	out["reached"] = at
}

// previewLineStart is the start of the line that holds byte at. It looks back at
// most previewLineMax bytes: a longer line is entered where the match is.
func previewLineStart(f *os.File, at int64) int64 {
	from := max(at-previewLineMax, 0)
	buf := make([]byte, at-from)
	n, _ := f.ReadAt(buf, from)
	if i := bytes.LastIndexByte(buf[:n], '\n'); i >= 0 {
		return from + int64(i) + 1
	}
	if from == 0 {
		return 0
	}
	return at
}

// describeBinary is the preview of a file that has none: its type and size.
func describeBinary(f *os.File, out map[string]any, note string) {
	out["kind"] = "binary"
	head := make([]byte, 512)
	n, _ := f.ReadAt(head, 0)
	out["mime"] = http.DetectContentType(head[:n])
	if note != "" {
		out["note"] = note
	}
}

func previewVerbs() map[string]verbEntry {
	return map[string]verbEntry{
		"file-preview": {
			description: "Make the preview of a file on this machine. An image comes scaled to max_px on its long side, a PDF page and a film frame as an image (with pdftoppm and ffmpeg there), text as its first 256 KiB, and anything else as its type and size.",
			params: []verbParam{
				{Name: "path", Type: "string", Required: true, Description: "The file. An absolute path, or one that starts with ~."},
				{Name: "max_px", Type: "int", Description: "The longest side of an image, at most 4096.", Default: "1600"},
				{Name: "page", Type: "int", Description: "The PDF page.", Default: "1"},
				{Name: "pages_known", Type: "bool", Description: "The caller knows the page count, so pdfinfo does not run.", Default: "false"},
				{Name: "words", Type: "bool", Description: "Send the PDF page's words from pdftotext in place of its picture.", Default: "false"},
				{Name: "offset", Type: "int", Description: "Send a window of a text file from this byte, whole lines only, in place of the preview."},
				{Name: "limit", Type: "int", Description: "The most bytes in that window, at most 1 MiB.", Default: "262144"},
				{Name: "find", Type: "string", Description: "Look for this text in a text file, in place of the preview. Lower case ignores case."},
				{Name: "from", Type: "int", Description: "The byte find starts at."},
				{Name: "back", Type: "bool", Description: "Find goes toward the start of the file."},
				{Name: "line", Type: "int", Description: "Find the start of this line number, in place of the preview."},
			},
			returns: []verbParam{
				{Name: "kind", Type: "string", Description: "image, svg, pdf, video, text, dir or binary.", Accepted: []string{"image", "svg", "pdf", "video", "text", "dir", "binary"}},
				{Name: "image", Type: "string", Description: "The picture, base64, as mime says: the image, the page or the frame."},
				{Name: "mime", Type: "string", Description: "The picture's type for image, pdf and video, and the file's type otherwise."},
				{Name: "width", Type: "int", Description: "An image's own width."},
				{Name: "height", Type: "int", Description: "An image's own height."},
				{Name: "text", Type: "string", Description: "text and svg: the content."},
				{Name: "truncated", Type: "bool", Description: "text: the file is longer than what was sent."},
				{Name: "offset", Type: "int", Description: "A window: the byte it starts at. find and line: the start of the line found, or -1."},
				{Name: "reached", Type: "int", Description: "find and line: how far the read went. Call again from there when the search ran out of time."},
				{Name: "counted", Type: "int", Description: "line: the lines passed."},
				{Name: "next_offset", Type: "int", Description: "A window: the byte the next window starts at."},
				{Name: "pdf", Type: "string", Description: "pdf, when this machine cannot draw the page: the file, base64, at most 8 MiB."},
				{Name: "pages", Type: "int", Description: "pdf: the page count, when pdfinfo is there."},
				{Name: "words", Type: "string", Description: "pdf, when asked for: the page's words."},
				{Name: "no_tool", Type: "string", Description: "pdf: the tool this machine needs and does not have."},
				{Name: "duration_ms", Type: "int", Description: "video: the length."},
				{Name: "note", Type: "string", Description: "Why there is no picture, in words a person can act on."},
				{Name: "size", Type: "int", Description: "The file's size."},
				{Name: "mtime", Type: "int", Description: "The file's modification time, Unix ms."},
			},
			examples: []string{`{"id":1,"verb":"file-preview","params":{"path":"~/Pictures/cat.jpg","max_px":1200}}`},
			handler:  (*Daemon).verbFilePreview,
		},
	}
}
