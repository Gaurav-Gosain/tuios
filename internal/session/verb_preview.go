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
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	_ "golang.org/x/image/bmp"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
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
	previewMaxPx       = 1600
	previewMaxPxLimit  = 4096
	previewTextMax     = 256 << 10
	previewImageMax    = 64 << 20
	previewImageDimMax = 10000
	previewPDFBytesMax = 8 << 20
	previewSVGMax      = 4 << 20
	previewToolTimeout = 20 * time.Second
	// previewDecodeMax bounds the memory one decoded image may take. A
	// small file can hold a huge picture: a 400 KB PNG of 10000 x 10000
	// pixels decodes to 400 MB, and the scale to more.
	previewDecodeMax = 256 << 20
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
		Path  string `json:"path"`
		MaxPx int    `json:"max_px"`
		Page  int    `json:"page"`
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
	out := map[string]any{
		"path":  path,
		"name":  filepath.Base(path),
		"size":  fi.Size(),
		"mtime": fi.ModTime().UnixMilli(),
	}
	if err != nil {
		// openRegular refused it and said what it is, without reading it.
		// A named pipe would hold the preview until something wrote to it,
		// and a device has no end. Neither is opened past that.
		if fi.IsDir() {
			out["kind"] = "dir"
			return out, nil
		}
		out["kind"] = "binary"
		out["note"] = "This is a pipe, a device or a socket. It has no preview."
		return out, nil
	}
	defer func() { _ = f.Close() }()
	ext := strings.ToLower(filepath.Ext(path))
	ctx, cancel := context.WithTimeout(d.ctx, previewToolTimeout)
	defer cancel()

	switch {
	case previewImageExt[ext]:
		if err := previewImage(ctx, f, fi.Size(), maxPx, out); err != nil {
			describeBinary(f, out, err.Error())
		}
		return out, nil
	case ext == ".svg":
		if fi.Size() <= previewSVGMax {
			if b, err := readAllOf(f, previewSVGMax); err == nil {
				out["kind"] = "svg"
				out["text"] = string(b)
				return out, nil
			}
		}
		describeBinary(f, out, "The drawing is too large to show.")
		return out, nil
	case ext == ".pdf":
		previewPDF(ctx, f, fi.Size(), maxPx, page, out)
		return out, nil
	case previewVideoExt[ext]:
		previewVideo(ctx, f, maxPx, out)
		return out, nil
	}
	if previewText(f, fi.Size(), out) {
		return out, nil
	}
	describeBinary(f, out, "")
	return out, nil
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
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.ExtraFiles = []*os.File{f}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return stdout.Bytes(), nil
}

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

// previewImage decodes an image and sends it at most maxPx on its long side.
func previewImage(ctx context.Context, f *os.File, size int64, maxPx int, out map[string]any) error {
	if size > previewImageMax {
		return errors.New("The image is too large to show.")
	}
	cfg, format, err := image.DecodeConfig(io.NewSectionReader(f, 0, size))
	if err != nil {
		return errors.New("The image could not be read.")
	}
	if cfg.Width > previewImageDimMax || cfg.Height > previewImageDimMax ||
		int64(cfg.Width)*int64(cfg.Height)*decodedBytesPerPixel(cfg.ColorModel) > previewDecodeMax {
		return errors.New("The image is too large to show.")
	}
	select {
	case previewDecodeSlot <- struct{}{}:
		defer func() { <-previewDecodeSlot }()
	case <-ctx.Done():
		return errors.New("The preview took too long. Try again.")
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
		return errors.New("The image could not be read.")
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

// fit scales img down to at most maxPx on its long side.
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
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
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
func previewPDF(ctx context.Context, f *os.File, size int64, maxPx, page int, out map[string]any) {
	out["kind"] = "pdf"
	if pages := pdfPages(ctx, f); pages > 0 {
		out["pages"] = pages
	}
	out["page"] = page
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
	if size <= previewPDFBytesMax {
		if b, err := readAllOf(f, previewPDFBytesMax); err == nil {
			out["pdf"] = base64.StdEncoding.EncodeToString(b)
			return
		}
	}
	out["note"] = "This machine has no pdftoppm, and the file is too large to send for a preview."
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
	args := []string{"-v", "error", "-nostdin"}
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
	b, err := previewTool(ctx, f, "ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=nw=1:nk=1", fdArg)
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
			},
			returns: []verbParam{
				{Name: "kind", Type: "string", Description: "image, svg, pdf, video, text, dir or binary.", Accepted: []string{"image", "svg", "pdf", "video", "text", "dir", "binary"}},
				{Name: "image", Type: "string", Description: "The picture, base64, as mime says: the image, the page or the frame."},
				{Name: "mime", Type: "string", Description: "The picture's type for image, pdf and video, and the file's type otherwise."},
				{Name: "width", Type: "int", Description: "An image's own width."},
				{Name: "height", Type: "int", Description: "An image's own height."},
				{Name: "text", Type: "string", Description: "text and svg: the content."},
				{Name: "truncated", Type: "bool", Description: "text: the file is longer than what was sent."},
				{Name: "pdf", Type: "string", Description: "pdf, when this machine cannot draw the page: the file, base64, at most 8 MiB."},
				{Name: "pages", Type: "int", Description: "pdf: the page count, when pdfinfo is there."},
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
