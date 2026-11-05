package tuie2e

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"
)

// file-preview: what Quick Look in the GUI shows, made on the machine the
// file is on. It reads a file's bytes, so it keeps the rules file-read keeps:
// the files capability, the read deny list, a file with one name, and one
// open that every later read and every tool works from.
//
// What would pass a weaker test and fail these: a preview that opens the path
// it was given without the link checks (the dummy key comes back as text), a
// preview that needs only list, a preview that hands a tool the path instead
// of the open file (the PDF and the film get no picture), and one with no cap
// on the decoded picture (the daemon's memory).

// filePreview is the part of a file-preview answer the tests read.
type filePreview struct {
	Kind        string `json:"kind"`
	Width       int    `json:"width"`
	ImageWidth  int    `json:"image_width"`
	ImageHeight int    `json:"image_height"`
	Image       string `json:"image"`
	Mime        string `json:"mime"`
	Text        string `json:"text"`
	Pages       int    `json:"pages"`
	DurationMs  int64  `json:"duration_ms"`
	Note        string `json:"note"`
	Page        int    `json:"page"`
	Words       string `json:"words"`
	AtMs        int64  `json:"at_ms"`
	VideoCodec  string `json:"video_codec"`
}

// minimalPDF is a one page PDF with a correct cross reference table.
func minimalPDF() []byte {
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 100] /Contents 4 0 R >>",
	}
	stream := "0 0 1 rg 20 20 160 60 re f"
	objs = append(objs, fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream))
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return b.Bytes()
}

// pagedPDF is a PDF of n pages. Each page has its own colour and its own words, so
// a test can tell which page it was sent.
func pagedPDF(n int) []byte {
	kids := make([]string, n)
	for i := range n {
		kids[i] = fmt.Sprintf("%d 0 R", 4+2*i)
	}
	objs := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), n),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	for i := range n {
		stream := fmt.Sprintf("0 0 %.2f rg 20 20 160 40 re f 0 0 0 rg BT /F1 12 Tf 20 75 Td (words of leaf %d) Tj ET", float64(i+1)/float64(n), i+1)
		objs = append(objs,
			fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 100] /Resources << /Font << /F1 3 0 R >> >> /Contents %d 0 R >>", 5+2*i),
			fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream))
	}
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return b.Bytes()
}

// TestFilePreviewOnAHost previews four kinds of file on build, through the
// hub's link: a 3000 px PNG comes back as a 1200 px picture smaller than the
// file, a table as text, a PDF page and a film frame as pictures that
// pdftoppm and ffmpeg made from the open file. The last two need those tools
// on this machine; the test says so and skips that half without them.
func TestFilePreviewOnAHost(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	farHome := xdgDir(remote, "HOME")
	writeRemoteConfig(t, remote, relayFilesPolicy)
	dir := filepath.Join(farHome, "work")
	writeFarFile(t, filepath.Join(dir, "data.csv"), "id,name\n1,ada\n2,linus\n", 0o644)
	img := image.NewRGBA(image.Rect(0, 0, 3000, 2000))
	for y := range 2000 {
		for x := range 3000 {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		t.Fatal(err)
	}
	writeFarFile(t, filepath.Join(dir, "wide.png"), pngBuf.String(), 0o644)
	writeFarFile(t, filepath.Join(dir, "page.pdf"), string(minimalPDF()), 0o644)
	writeFarFile(t, filepath.Join(dir, "book.pdf"), string(pagedPDF(3)), 0o644)
	film := ""
	if ff, err := exec.LookPath("ffmpeg"); err == nil {
		film = filepath.Join(dir, "clip.mp4")
		out, err := exec.Command(ff, "-v", "error", "-f", "lavfi", "-i", "testsrc=duration=2:size=320x240:rate=10", "-pix_fmt", "yuv420p", "-g", "5", film).CombinedOutput()
		if err != nil {
			t.Fatalf("make a test film: %v\n%s", err, out)
		}
	}
	hubWithFileHost(t, base, remote, writeFakeSSHTo(t, base, remote))
	far := dialHostVerbs(t, base)
	record := map[string]any{}

	var pic filePreview
	far.must("file-preview", map[string]any{"path": "~/work/wide.png", "max_px": 1200}, &pic)
	record["png"] = map[string]any{"kind": pic.Kind, "width": pic.Width, "image": []int{pic.ImageWidth, pic.ImageHeight}, "bytes": len(pic.Image)}
	if pic.Kind != "image" || pic.Width != 3000 || pic.ImageWidth != 1200 || pic.ImageHeight != 800 {
		t.Fatalf("ASSERTION: the image preview is %s %dpx scaled to %dx%d, want image 3000px scaled to 1200x800", pic.Kind, pic.Width, pic.ImageWidth, pic.ImageHeight)
	}
	raw, err := base64.StdEncoding.DecodeString(pic.Image)
	if err != nil {
		t.Fatal(err)
	}
	if cfg, err := png.DecodeConfig(bytes.NewReader(raw)); err != nil || cfg.Width != 1200 {
		t.Fatalf("ASSERTION: the preview is not a 1200 px PNG: %+v %v", cfg, err)
	}
	if len(raw) >= pngBuf.Len() {
		t.Fatalf("ASSERTION: the preview (%d bytes) is not smaller than the file (%d bytes)", len(raw), pngBuf.Len())
	}

	var text filePreview
	far.must("file-preview", map[string]any{"path": "~/work/data.csv"}, &text)
	record["csv"] = text
	if text.Kind != "text" || !strings.Contains(text.Text, "2,linus") {
		t.Fatalf("ASSERTION: the table preview is %+v", text)
	}

	// A window starts at the next line and holds whole lines.
	var window struct {
		Kind       string `json:"kind"`
		Text       string `json:"text"`
		Offset     int64  `json:"offset"`
		NextOffset int64  `json:"next_offset"`
		Truncated  bool   `json:"truncated"`
	}
	far.must("file-preview", map[string]any{"path": "~/work/data.csv", "offset": 5, "limit": 10}, &window)
	record["csv-window"] = window
	if window.Kind != "text" || window.Text != "1,ada\n" || window.Offset != 8 || window.NextOffset != 14 || !window.Truncated {
		t.Fatalf("ASSERTION: the window of the table is %+v", window)
	}

	if _, err := exec.LookPath("pdftoppm"); err != nil {
		t.Logf("no pdftoppm here: the PDF half is skipped")
	} else {
		var pdf filePreview
		far.must("file-preview", map[string]any{"path": "~/work/page.pdf", "max_px": 400}, &pdf)
		record["pdf"] = map[string]any{"kind": pdf.Kind, "pages": pdf.Pages, "image": []int{pdf.ImageWidth, pdf.ImageHeight}, "mime": pdf.Mime, "note": pdf.Note}
		if pdf.Kind != "pdf" || pdf.Image == "" || pdf.Mime != "image/png" || pdf.ImageWidth != 400 {
			t.Errorf("ASSERTION: the PDF page was not drawn from the open file: kind %s mime %s %dx%d note %q", pdf.Kind, pdf.Mime, pdf.ImageWidth, pdf.ImageHeight, pdf.Note)
		}
		if _, err := exec.LookPath("pdfinfo"); err == nil && pdf.Pages != 1 {
			t.Errorf("ASSERTION: pdfinfo did not count the page of the open file: %d", pdf.Pages)
		}
		// A later page with the count known: no pdfinfo, and the page asked for.
		var page filePreview
		far.must("file-preview", map[string]any{"path": "~/work/book.pdf", "max_px": 400, "page": 2, "pages_known": true}, &page)
		if page.Page != 2 || page.Pages != 0 || page.Image == "" {
			t.Errorf("ASSERTION: a page with the count known: page %d, pages %d, image %d bytes, note %q", page.Page, page.Pages, len(page.Image), page.Note)
		}
		if _, err := exec.LookPath("pdftotext"); err == nil {
			var words filePreview
			far.must("file-preview", map[string]any{"path": "~/work/book.pdf", "page": 3, "pages_known": true, "words": true}, &words)
			if !strings.Contains(words.Words, "words of leaf 3") || words.Image != "" {
				t.Errorf("ASSERTION: the words of page 3 did not come from the open file: %q, image %d bytes", words.Words, len(words.Image))
			}
		}
	}
	if film == "" {
		t.Logf("no ffmpeg here: the film half is skipped")
	} else {
		// A clip with one key frame has none past its start, so the frame must come
		// from a decode. This was "No frame could be read" before.
		one := filepath.Join(dir, "one-key.mp4")
		if b, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc=duration=2:size=160x120:rate=10", "-g", "1000", "-pix_fmt", "yuv420p", one).CombinedOutput(); err != nil {
			t.Fatalf("make a film with one key frame: %v\n%s", err, b)
		}
		var key filePreview
		far.must("file-preview", map[string]any{"path": "~/work/one-key.mp4", "max_px": 160}, &key)
		if key.Image == "" {
			t.Errorf("ASSERTION: a clip with one key frame gave no frame: note %q", key.Note)
		}
		// A place in the film, with the facts known: no ffprobe, and it says where.
		var seek filePreview
		far.must("file-preview", map[string]any{"path": "~/work/clip.mp4", "max_px": 160, "at_ms": 1500, "duration_known": true}, &seek)
		if seek.AtMs != 1500 || seek.DurationMs != 0 || seek.Image == "" {
			t.Errorf("ASSERTION: a seek with the facts known: at %d, duration %d, image %d bytes, note %q", seek.AtMs, seek.DurationMs, len(seek.Image), seek.Note)
		}
		// Past the last frame, which starts before the film's end.
		var last filePreview
		far.must("file-preview", map[string]any{"path": "~/work/clip.mp4", "max_px": 160, "at_ms": 1995, "duration_known": true}, &last)
		if last.Image == "" {
			t.Errorf("ASSERTION: the end of a film gave no frame: note %q", last.Note)
		}
		var facts filePreview
		far.must("file-preview", map[string]any{"path": "~/work/clip.mp4", "no_frame": true}, &facts)
		if facts.VideoCodec != "h264" || facts.Image != "" || facts.DurationMs == 0 {
			t.Errorf("ASSERTION: only the facts: codec %q, duration %d, image %d bytes", facts.VideoCodec, facts.DurationMs, len(facts.Image))
		}
		var vid filePreview
		far.must("file-preview", map[string]any{"path": "~/work/clip.mp4", "max_px": 160}, &vid)
		record["film"] = map[string]any{"kind": vid.Kind, "duration_ms": vid.DurationMs, "image": []int{vid.ImageWidth, vid.ImageHeight}, "note": vid.Note}
		if vid.Kind != "video" || vid.Image == "" || vid.ImageWidth != 160 {
			t.Errorf("ASSERTION: no frame came from the open film: kind %s %dx%d note %q", vid.Kind, vid.ImageWidth, vid.ImageHeight, vid.Note)
		}
		if _, err := exec.LookPath("ffprobe"); err == nil && (vid.DurationMs < 1500 || vid.DurationMs > 2500) {
			t.Errorf("ASSERTION: ffprobe did not read the length of the open film: %d ms", vid.DurationMs)
		}
	}
	saveTransferArtifact(t, "file-preview-on-a-host", record)
}

// TestFilePreviewKeepsToTheReadRules asks build for previews a linked machine
// may not have: a dummy key under ~/.ssh, the same key through a symlink and
// through a hard link in the receive folder, and the ~/.ssh folder. Each must
// be refused as forbidden and the marker must never come back. The positive
// half previews an ordinary file in the same folder.
func TestFilePreviewKeepsToTheReadRules(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	farHome := xdgDir(remote, "HOME")
	writeRemoteConfig(t, remote, relayFilesPolicy)
	key := dummyKey(t, farHome)
	recv := filepath.Join(farHome, "Downloads", "tuios")
	writeFarFile(t, filepath.Join(recv, "notes.txt"), "ordinary\n", 0o644)
	if err := os.Symlink(key, filepath.Join(recv, "keyref.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(key, filepath.Join(recv, "keylink.txt")); err != nil {
		t.Fatal(err)
	}
	hubWithFileHost(t, base, remote, writeFakeSSHTo(t, base, remote))
	record := map[string]any{}

	for _, path := range []string{"~/.ssh/id_ed25519", "~/Downloads/tuios/keyref.txt", "~/Downloads/tuios/keylink.txt", "~/.ssh"} {
		c := dialHostVerbs(t, base)
		raw, err := c.call("file-preview", map[string]any{"path": path})
		record[path] = map[string]any{"error": errString(err), "answer": string(raw)}
		if !forbidden(err) {
			t.Errorf("ASSERTION: a preview of %s over the link was not refused as forbidden: %v %s", path, err, raw)
		}
		if strings.Contains(string(raw), "DUMMY-KEY-MARKER") {
			t.Errorf("ASSERTION: the preview of %s returned the dummy key", path)
		}
		_ = c.conn.Close()
	}

	var text filePreview
	dialHostVerbs(t, base).must("file-preview", map[string]any{"path": "~/Downloads/tuios/notes.txt"}, &text)
	record["notes.txt"] = text
	if text.Kind != "text" || text.Text != "ordinary\n" {
		t.Errorf("ASSERTION: an ordinary file did not preview: %+v", text)
	}
	if b, _ := os.ReadFile(key); string(b) != dummyKeyMarker {
		t.Errorf("ASSERTION: the dummy key changed: %q", b)
	}
	saveTransferArtifact(t, "file-preview-read-rules", record)
}

// TestFilePreviewNeedsTheFilesCapability gives the hub only list on build. A
// listing goes through, a preview of a file in that folder is forbidden.
func TestFilePreviewNeedsTheFilesCapability(t *testing.T) {
	base := t.TempDir()
	remote := remoteMachine(t)
	farHome := xdgDir(remote, "HOME")
	writeRemoteConfig(t, remote, "[hosts.\"*\"]\nallow = [\"list\"]\n")
	writeFarFile(t, filepath.Join(farHome, "secret.txt"), "not for the hub\n", 0o600)
	hubWithFileHost(t, base, remote, writeFakeSSHTo(t, base, remote))

	far := dialHostVerbs(t, base)
	far.must("file-list", map[string]any{"dir": "~"}, nil)
	raw, err := far.call("file-preview", map[string]any{"path": "~/secret.txt"})
	saveTransferArtifact(t, "file-preview-list-only", map[string]any{"error": errString(err), "answer": string(raw)})
	if !forbidden(err) || strings.Contains(string(raw), "not for the hub") {
		t.Fatalf("ASSERTION: a list-only link previewed a file's bytes: %v %s", err, raw)
	}
}

// daemonPeakKB is the most memory the daemon of base has held, from /proc.
func daemonPeakKB(t *testing.T, base string) int64 {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(daemonPID(t, base)), "status"))
	if err != nil {
		t.Skipf("no /proc here: %v", err)
	}
	for line := range strings.SplitSeq(string(b), "\n") {
		if rest, ok := strings.CutPrefix(line, "VmHWM:"); ok {
			n, _ := strconv.ParseInt(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest), "kB")), 10, 64)
			return n
		}
	}
	t.Fatalf("no VmHWM in the daemon's status")
	return 0
}

// writePNGBomb writes a PNG of w x h transparent pixels. The rows are all
// zero, so the file is small and the picture is not.
func writePNGBomb(t *testing.T, path string, w, h int) {
	t.Helper()
	var idat bytes.Buffer
	zw, _ := zlib.NewWriterLevel(&idat, zlib.BestCompression)
	row := make([]byte, 1+w*4)
	for range h {
		_, _ = zw.Write(row)
	}
	_ = zw.Close()
	chunk := func(out *bytes.Buffer, typ string, data []byte) {
		_ = binary.Write(out, binary.BigEndian, uint32(len(data)))
		out.WriteString(typ)
		out.Write(data)
		_ = binary.Write(out, binary.BigEndian, crc32.ChecksumIEEE(append([]byte(typ), data...)))
	}
	var out bytes.Buffer
	out.WriteString("\x89PNG\r\n\x1a\n")
	var hdr bytes.Buffer
	_ = binary.Write(&hdr, binary.BigEndian, uint32(w))
	_ = binary.Write(&hdr, binary.BigEndian, uint32(h))
	hdr.Write([]byte{8, 6, 0, 0, 0})
	chunk(&out, "IHDR", hdr.Bytes())
	chunk(&out, "IDAT", idat.Bytes())
	chunk(&out, "IEND", nil)
	if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestFilePreviewOfAPipeADeviceAndAHugePicture previews a named pipe, which
// blocks whoever opens it to read, /dev/zero, which never ends, and a 400 KB
// PNG that decodes to 400 MB. Each must answer at once, and four previews of
// the picture together must not take the daemon past 512 MB. An ordinary
// picture still previews.
func TestFilePreviewOfAPipeADeviceAndAHugePicture(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "home", "--detach"); err != nil {
		t.Fatalf("start the daemon: %v\n%s", err, out)
	}
	pipe := filepath.Join(base, "pipe")
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Skipf("no named pipes here: %v", err)
	}
	probes := map[string]string{}
	for _, path := range []string{pipe, "/dev/zero"} {
		c := dialFileVerbs(t, base)
		raw, err := c.callWithin(5*time.Second, "file-preview", map[string]any{"path": path})
		var ne net.Error
		if errors.As(err, &ne) && ne.Timeout() {
			t.Errorf("ASSERTION: file-preview of %s did not answer in 5 s", path)
		}
		var p filePreview
		_ = json.Unmarshal(raw, &p)
		probes[path] = string(raw) + errString(err)
		if err == nil && p.Kind != "binary" {
			t.Errorf("ASSERTION: the preview of %s is %q, want binary", path, p.Kind)
		}
	}

	bomb := filepath.Join(base, "huge.png")
	writePNGBomb(t, bomb, 10000, 10000)
	var wg sync.WaitGroup
	answers := make([]string, 4)
	for i := range answers {
		wg.Go(func() {
			c := dialFileVerbs(t, base)
			raw, err := c.callWithin(60*time.Second, "file-preview", map[string]any{"path": bomb})
			var out filePreview
			_ = json.Unmarshal(raw, &out)
			answers[i] = fmt.Sprintf("%s %s %v", out.Kind, out.Note, err)
		})
	}
	wg.Wait()
	peak := daemonPeakKB(t, base)

	small := filepath.Join(base, "small.png")
	var buf bytes.Buffer
	_ = png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2000, 1000)))
	_ = os.WriteFile(small, buf.Bytes(), 0o644)
	var pic filePreview
	dialFileVerbs(t, base).must("file-preview", map[string]any{"path": small, "max_px": 500}, &pic)

	saveTransferArtifact(t, "file-preview-not-a-file", map[string]any{"probes": probes, "bomb_previews": answers, "daemon_peak_kb": peak, "small": []int{pic.ImageWidth, pic.ImageHeight}})
	t.Logf("daemon peak %d kB; bomb previews %q", peak, answers)
	if peak > 512<<10 {
		t.Errorf("ASSERTION: four previews of a 10000 x 10000 PNG took the daemon to %d MB", peak>>10)
	}
	if pic.Kind != "image" || pic.ImageWidth != 500 {
		t.Errorf("ASSERTION: an ordinary picture no longer previews: %+v", pic.Kind)
	}
}

// TestFilePreviewHoldsItsDecodeCap previews pictures whose decode costs more
// than the pixels say, and checks the daemon's memory. A 9000 x 7000
// progressive JPEG keeps every block's coefficients while it decodes: Go's
// decoder took the daemon to 1.25 GB for one, past the 256 MB cap the pixel
// count alone allowed. It is now sent as it is for the client to decode. A
// baseline JPEG that fits is still decoded and scaled here, and a WebP is
// sent as it is. ImageMagick makes the progressive file; without it that half
// is skipped.
func TestFilePreviewHoldsItsDecodeCap(t *testing.T) {
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "home", "--detach"); err != nil {
		t.Fatalf("start the daemon: %v\n%s", err, out)
	}
	record := map[string]any{}
	c := dialFileVerbs(t, base)

	// A baseline JPEG that fits: decoded and scaled here.
	small := filepath.Join(base, "photo.jpg")
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 3000, 2000)), &jpeg.Options{Quality: 80})
	if err := os.WriteFile(small, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	var pic filePreview
	c.must("file-preview", map[string]any{"path": small, "max_px": 1200}, &pic)
	record["baseline"] = []any{pic.Kind, pic.Mime, pic.ImageWidth, pic.ImageHeight}
	if pic.Kind != "image" || pic.ImageWidth != 1200 || pic.ImageHeight != 800 {
		t.Errorf("ASSERTION: a baseline JPEG that fits was not scaled here: %s %s %dx%d", pic.Kind, pic.Mime, pic.ImageWidth, pic.ImageHeight)
	}

	magick, err := exec.LookPath("magick")
	if err != nil {
		t.Skipf("no ImageMagick here: the progressive JPEG and the WebP are skipped")
	}
	prog := filepath.Join(base, "huge.jpg")
	if out, err := exec.Command(magick, "-size", "9000x7000", "xc:gray50", "-interlace", "JPEG", prog).CombinedOutput(); err != nil {
		t.Fatalf("make the progressive JPEG: %v\n%s", err, out)
	}
	webp := filepath.Join(base, "pic.webp")
	if out, err := exec.Command(magick, "-size", "64x48", "xc:red", webp).CombinedOutput(); err != nil {
		t.Fatalf("make the WebP: %v\n%s", err, out)
	}
	peak0 := daemonPeakKB(t, base)
	var big filePreview
	c.must("file-preview", map[string]any{"path": prog, "max_px": 1600}, &big)
	peak := daemonPeakKB(t, base)
	file, _ := os.ReadFile(prog)
	sent, _ := base64.StdEncoding.DecodeString(big.Image)
	record["progressive"] = map[string]any{"kind": big.Kind, "mime": big.Mime, "width": big.Width, "sent_bytes": len(sent), "file_bytes": len(file), "peak_kb_before": peak0, "peak_kb_after": peak}
	t.Logf("daemon peak %d kB before, %d kB after the progressive JPEG", peak0, peak)
	if peak > 300<<10 {
		t.Errorf("ASSERTION: a preview of a 9000 x 7000 progressive JPEG took the daemon to %d MB", peak>>10)
	}
	if big.Kind != "image" || big.Mime != "image/jpeg" || !bytes.Equal(sent, file) || big.Width != 9000 {
		t.Errorf("ASSERTION: the progressive JPEG was not sent as it is for the client: %s %s width %d, %d of %d bytes", big.Kind, big.Mime, big.Width, len(sent), len(file))
	}

	// A grey baseline JPEG of the same size fits the cap: one byte a pixel.
	// It is decoded and scaled here, and the scale must not build a buffer
	// of the scaled width by the source's height (CatmullRom's, 358 MB).
	grey := filepath.Join(base, "grey.jpg")
	if out, err := exec.Command(magick, "-size", "9000x7000", "xc:gray50", "-colorspace", "Gray", "-interlace", "none", grey).CombinedOutput(); err != nil {
		t.Fatalf("make the grey JPEG: %v\n%s", err, out)
	}
	var g filePreview
	c.must("file-preview", map[string]any{"path": grey, "max_px": 1600}, &g)
	gpeak := daemonPeakKB(t, base)
	record["grey"] = map[string]any{"kind": g.Kind, "image": []int{g.ImageWidth, g.ImageHeight}, "peak_kb": gpeak}
	t.Logf("daemon peak %d kB after the grey JPEG", gpeak)
	if g.Kind != "image" || g.ImageWidth != 1600 {
		t.Errorf("ASSERTION: a grey 9000 x 7000 baseline JPEG was not scaled here: %s %dx%d", g.Kind, g.ImageWidth, g.ImageHeight)
	}
	if gpeak > 300<<10 {
		t.Errorf("ASSERTION: scaling a 9000 x 7000 picture took the daemon to %d MB", gpeak>>10)
	}

	var w filePreview
	c.must("file-preview", map[string]any{"path": webp}, &w)
	wfile, _ := os.ReadFile(webp)
	wsent, _ := base64.StdEncoding.DecodeString(w.Image)
	record["webp"] = []any{w.Kind, w.Mime, len(wsent)}
	if w.Kind != "image" || w.Mime != "image/webp" || !bytes.Equal(wsent, wfile) {
		t.Errorf("ASSERTION: the WebP was not sent as it is: %s %s %d bytes", w.Kind, w.Mime, len(wsent))
	}
	saveTransferArtifact(t, "file-preview-decode-cap", record)
}

// craftedPreview is the part of a file-preview answer the crafted files are
// read by: a listing, a JSON layout and a GIF's frames.
type craftedPreview struct {
	Kind    string `json:"kind"`
	Entries []struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
		Dir  bool   `json:"dir"`
	} `json:"entries"`
	Capped    bool   `json:"capped"`
	Formatted bool   `json:"formatted"`
	Text      string `json:"text"`
	Pages     int    `json:"pages"`
	Page      int    `json:"page"`
}

// summary is what the artifact keeps of an answer.
func (p craftedPreview) summary() map[string]any {
	s := map[string]any{"kind": p.Kind, "entries": len(p.Entries), "capped": p.Capped, "formatted": p.Formatted, "text_bytes": len(p.Text), "pages": p.Pages, "page": p.Page}
	if n := len(p.Entries); n > 0 {
		s["first_name_bytes"], s["last_name_bytes"] = len(p.Entries[0].Name), len(p.Entries[n-1].Name)
	}
	return s
}

// names is the names of a listing, in its order.
func (p craftedPreview) names() []string {
	out := make([]string, len(p.Entries))
	for i, e := range p.Entries {
		out[i] = e.Name
	}
	return out
}

// manyEntryName is the name of entry i of writeManyEntryZip's zip.
func manyEntryName(i int) string { return fmt.Sprintf("e%07d.txt", i) }

// writeManyEntryZip writes a zip64 of n empty stored files, by hand: a local
// header for each, the central directory, the zip64 end record and its
// locator, and an end record whose count says to read them. zip.Writer
// would hold every header in the test's memory.
func writeManyEntryZip(t *testing.T, path string, n int) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := bufio.NewWriterSize(f, 1<<20)
	le := binary.LittleEndian
	var off uint64
	at := make([]uint32, n)
	for i := range n {
		name := manyEntryName(i)
		h := make([]byte, 30, 30+len(name))
		le.PutUint32(h[0:], 0x04034b50)
		le.PutUint16(h[4:], 20)
		le.PutUint16(h[12:], 0x21)
		le.PutUint16(h[26:], uint16(len(name)))
		_, _ = w.Write(append(h, name...))
		at[i] = uint32(off)
		off += uint64(len(h) + len(name))
	}
	dirAt := off
	for i := range n {
		name := manyEntryName(i)
		h := make([]byte, 46, 46+len(name))
		le.PutUint32(h[0:], 0x02014b50)
		le.PutUint16(h[4:], 20)
		le.PutUint16(h[6:], 20)
		le.PutUint16(h[14:], 0x21)
		le.PutUint16(h[28:], uint16(len(name)))
		le.PutUint32(h[42:], at[i])
		_, _ = w.Write(append(h, name...))
		off += uint64(len(h) + len(name))
	}
	dirSize := off - dirAt
	end64 := make([]byte, 56)
	le.PutUint32(end64[0:], 0x06064b50)
	le.PutUint64(end64[4:], 44)
	le.PutUint16(end64[12:], 45)
	le.PutUint16(end64[14:], 45)
	le.PutUint64(end64[24:], uint64(n))
	le.PutUint64(end64[32:], uint64(n))
	le.PutUint64(end64[40:], dirSize)
	le.PutUint64(end64[48:], dirAt)
	loc := make([]byte, 20)
	le.PutUint32(loc[0:], 0x07064b50)
	le.PutUint64(loc[8:], off)
	le.PutUint32(loc[16:], 1)
	end := make([]byte, 22)
	le.PutUint32(end[0:], 0x06054b50)
	le.PutUint16(end[8:], 0xffff)
	le.PutUint16(end[10:], 0xffff)
	le.PutUint32(end[12:], uint32(dirSize))
	le.PutUint32(end[16:], uint32(dirAt))
	for _, b := range [][]byte{end64, loc, end} {
		_, _ = w.Write(b)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// smallZip is a zip of empty files with these names, behind prefix. With at,
// the zip's offsets count the prefix, as zip.Writer.SetOffset makes them;
// without, they start at the zip, as a self-extracting archive has them.
func smallZip(t *testing.T, prefix []byte, at bool, names ...string) []byte {
	t.Helper()
	var b bytes.Buffer
	b.Write(prefix)
	zw := zip.NewWriter(&b)
	if at {
		zw.SetOffset(int64(len(prefix)))
	}
	for _, name := range names {
		if _, err := zw.Create(name); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// TestFilePreviewHoldsCraftedFilesToItsBounds previews small files made to
// cost a great deal, each in a daemon of its own, on the daemon socket as the
// GUI asks. A zip that names a million files must be listed from its first
// 2000 headers, four previews at once keeping the daemon under 256 MB (they
// took it past 1 GB reading the whole directory). A GIF whose frames are
// small but whose screen is 10000 x 10000 must not be stepped: the canvas and
// its copy are 800 MB. A JSON file nested 10000 deep, 20 KB, lays out to
// 200 MB, and must come back as it is. An archive's names are cut to 4096
// bytes, at a character, with "...". Each case has its ordinary file in the
// same daemon: a zip in its own order and behind a prefix, a GIF that steps,
// JSON that is laid out, and names that are listed whole.
func TestFilePreviewHoldsCraftedFilesToItsBounds(t *testing.T) {
	daemon := func(t *testing.T) string {
		base := t.TempDir()
		killDaemon(t, base)
		if out, err := tuiosCLI(t, base, "new", "home", "--detach"); err != nil {
			t.Fatalf("start the daemon: %v\n%s", err, out)
		}
		return base
	}
	preview := func(t *testing.T, base string, params map[string]any) craftedPreview {
		t.Helper()
		var p craftedPreview
		dialFileVerbs(t, base).must("file-preview", params, &p)
		return p
	}

	t.Run("zip", func(t *testing.T) {
		base := daemon(t)
		record := map[string]any{}
		many := filepath.Join(base, "many.zip")
		writeManyEntryZip(t, many, 1_000_000)
		answers := make([]craftedPreview, 4)
		errs := make([]error, 4)
		// The connections are made here: a failed dial stops the test, which
		// only the test's own goroutine may do.
		conns := make([]*fileVerbConn, len(answers))
		for i := range conns {
			conns[i] = dialFileVerbs(t, base)
		}
		var wg sync.WaitGroup
		for i := range answers {
			wg.Go(func() {
				raw, err := conns[i].call("file-preview", map[string]any{"path": many})
				if err == nil {
					err = json.Unmarshal(raw, &answers[i])
				}
				errs[i] = err
			})
		}
		wg.Wait()
		peak := daemonPeakKB(t, base)
		record["million"] = answers[0].summary()
		record["daemon_peak_kb"] = peak
		t.Logf("daemon peak %d kB after four previews of a zip of a million files", peak)
		for i, p := range answers {
			if errs[i] != nil {
				t.Errorf("ASSERTION: a preview of the zip of a million files failed: %v", errs[i])
				continue
			}
			if p.Kind != "archive" || len(p.Entries) != 2000 || !p.Capped {
				t.Errorf("ASSERTION: the zip of a million files is %s with %d entries, capped %v, want archive with 2000, capped", p.Kind, len(p.Entries), p.Capped)
				continue
			}
			if p.Entries[0].Name != manyEntryName(0) || p.Entries[1999].Name != manyEntryName(1999) {
				t.Errorf("ASSERTION: the zip of a million files lists %q first and %q 2000th, want %q and %q", p.Entries[0].Name, p.Entries[1999].Name, manyEntryName(0), manyEntryName(1999))
			}
		}
		if peak > 256<<10 {
			t.Errorf("ASSERTION: four previews of a zip of a million files took the daemon to %d MB", peak>>10)
		}

		// The order is the central directory's, not the names'.
		names := []string{"notes.txt", "app/", "app/main.go"}
		stub := bytes.Repeat([]byte{'#'}, 100)
		for _, c := range []struct {
			name string
			body []byte
		}{
			{"small.zip", smallZip(t, nil, false, names...)},
			{"prefixed.zip", smallZip(t, stub, false, names...)},
			{"offset.zip", smallZip(t, stub, true, names...)},
		} {
			path := filepath.Join(base, c.name)
			if err := os.WriteFile(path, c.body, 0o644); err != nil {
				t.Fatal(err)
			}
			p := preview(t, base, map[string]any{"path": path})
			record[c.name] = p.summary()
			if got := p.names(); p.Kind != "archive" || p.Capped || strings.Join(got, "|") != strings.Join(names, "|") {
				t.Errorf("ASSERTION: %s is %s %q, capped %v, want archive %q, not capped", c.name, p.Kind, got, p.Capped, names)
				continue
			}
			// app/ is a folder; the files are empty.
			for i, e := range p.Entries {
				if dir := strings.HasSuffix(names[i], "/"); e.Dir != dir || e.Size != 0 {
					t.Errorf("ASSERTION: %s lists %s as folder %v, %d bytes, want folder %v, 0 bytes", c.name, e.Name, e.Dir, e.Size, dir)
				}
			}
		}

		// A directory that ends before the count its end record gives is not
		// a zip, and shows as a binary file: the small zip above, whose end
		// record says 5 for its 3 headers.
		short := smallZip(t, nil, false, names...)
		le := binary.LittleEndian
		le.PutUint16(short[len(short)-22+8:], 5)
		le.PutUint16(short[len(short)-22+10:], 5)
		shortPath := filepath.Join(base, "short.zip")
		if err := os.WriteFile(shortPath, short, 0o644); err != nil {
			t.Fatal(err)
		}
		p := preview(t, base, map[string]any{"path": shortPath})
		record["short.zip"] = p.summary()
		if p.Kind != "binary" {
			t.Errorf("ASSERTION: a zip of 3 headers whose end record says 5 is %s with %d entries, capped %v, want binary", p.Kind, len(p.Entries), p.Capped)
		}

		// The end record keeps its count modulo 65536 when there is no zip64
		// record: a zip of 65537 files says 1, and is still capped.
		wide := filepath.Join(base, "wide.zip")
		files := 65537
		writeManyEntryZip(t, wide, files)
		f, err := os.OpenFile(wide, os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		fi, err := f.Stat()
		if err != nil {
			t.Fatal(err)
		}
		// Swap the zip64 end record, its locator and the end record for one
		// plain end record.
		tail := make([]byte, 56+20+22)
		at := fi.Size() - int64(len(tail))
		if _, err := f.ReadAt(tail, at); err != nil {
			t.Fatal(err)
		}
		end := make([]byte, 22)
		le.PutUint32(end[0:], 0x06054b50)
		le.PutUint16(end[8:], uint16(files))
		le.PutUint16(end[10:], uint16(files))
		le.PutUint32(end[12:], uint32(le.Uint64(tail[40:])))
		le.PutUint32(end[16:], uint32(le.Uint64(tail[48:])))
		if _, err := f.WriteAt(end, at); err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(at + int64(len(end))); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		p = preview(t, base, map[string]any{"path": wide})
		record["wide.zip"] = p.summary()
		if p.Kind != "archive" || len(p.Entries) != 2000 || !p.Capped {
			t.Errorf("ASSERTION: a zip of 65537 files whose end record says 1 is %s with %d entries, capped %v, want archive with 2000, capped", p.Kind, len(p.Entries), p.Capped)
		} else if p.Entries[1999].Name != manyEntryName(1999) {
			t.Errorf("ASSERTION: a zip of 65537 files lists %q 2000th, want %q", p.Entries[1999].Name, manyEntryName(1999))
		}

		// The cut is at 2000 entries: 2000 are all of them, 2001 are capped.
		for _, n := range []int{2000, 2001} {
			list := make([]string, n)
			for i := range list {
				list[i] = manyEntryName(i)
			}
			path := filepath.Join(base, fmt.Sprintf("n%d.zip", n))
			if err := os.WriteFile(path, smallZip(t, nil, false, list...), 0o644); err != nil {
				t.Fatal(err)
			}
			p := preview(t, base, map[string]any{"path": path})
			record[fmt.Sprintf("n%d", n)] = p.summary()
			if len(p.Entries) != 2000 || p.Capped != (n > 2000) {
				t.Errorf("ASSERTION: a zip of %d files lists %d entries, capped %v, want 2000, capped %v", n, len(p.Entries), p.Capped, n > 2000)
			} else if p.Entries[1999].Name != manyEntryName(1999) {
				t.Errorf("ASSERTION: a zip of %d files lists %q 2000th, want %q", n, p.Entries[1999].Name, manyEntryName(1999))
			}
		}
		saveTransferArtifact(t, "file-preview-crafted-zip", record)
	})

	t.Run("gif", func(t *testing.T) {
		base := daemon(t)
		record := map[string]any{}
		pal := color.Palette{color.Black, color.White}
		writeGIF := func(name string, w, h int, frames []*image.Paletted, disposal byte) string {
			g := &gif.GIF{Image: frames, Config: image.Config{ColorModel: pal, Width: w, Height: h}}
			for range frames {
				g.Delay = append(g.Delay, 10)
				g.Disposal = append(g.Disposal, disposal)
			}
			var b bytes.Buffer
			if err := gif.EncodeAll(&b, g); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(base, name)
			if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
			record[name+"_bytes"] = b.Len()
			return path
		}
		dot := func(w, h int, c uint8) *image.Paletted {
			m := image.NewPaletted(image.Rect(0, 0, w, h), pal)
			for i := range m.Pix {
				m.Pix[i] = c
			}
			return m
		}

		huge := writeGIF("screen.gif", 10000, 10000, []*image.Paletted{dot(1, 1, 1), dot(1, 1, 0)}, gif.DisposalPrevious)
		p := preview(t, base, map[string]any{"path": huge, "page": 2})
		record["screen"] = p.summary()
		if p.Pages != 0 || p.Page == 2 {
			t.Errorf("ASSERTION: a GIF of two 1 x 1 frames on a 10000 x 10000 screen was stepped to frame %d of %d, want its first frame only", p.Page, p.Pages)
		}

		small := writeGIF("spin.gif", 40, 40, []*image.Paletted{dot(40, 40, 0), dot(40, 40, 1), dot(40, 40, 0)}, gif.DisposalNone)
		q := preview(t, base, map[string]any{"path": small, "page": 2})
		record["spin"] = q.summary()
		if q.Kind != "image" || q.Pages != 3 || q.Page != 2 {
			t.Errorf("ASSERTION: a 40 x 40 GIF of 3 frames gave %s frame %d of %d, want image frame 2 of 3", q.Kind, q.Page, q.Pages)
		}
		record["daemon_peak_kb"] = daemonPeakKB(t, base)
		saveTransferArtifact(t, "file-preview-crafted-gif", record)
	})

	t.Run("json", func(t *testing.T) {
		base := daemon(t)
		record := map[string]any{}
		deep := filepath.Join(base, "deep.json")
		nested := strings.Repeat("[", 10000) + "0" + strings.Repeat("]", 10000)
		if err := os.WriteFile(deep, []byte(nested), 0o644); err != nil {
			t.Fatal(err)
		}
		p := preview(t, base, map[string]any{"path": deep, "format": true})
		record["deep"] = p.summary()
		if p.Formatted {
			t.Errorf("ASSERTION: JSON nested 10000 deep was laid out, %d bytes of text", len(p.Text))
		}

		var flat bytes.Buffer
		flat.WriteByte('[')
		for i := 0; flat.Len() < 900<<10; i++ {
			if i > 0 {
				flat.WriteByte(',')
			}
			fmt.Fprintf(&flat, `{"id":%d,"name":"row %d","tags":["a","b"]}`, i, i)
		}
		flat.WriteByte(']')
		var want bytes.Buffer
		if err := json.Indent(&want, flat.Bytes(), "", "  "); err != nil {
			t.Fatal(err)
		}
		plain := filepath.Join(base, "rows.json")
		if err := os.WriteFile(plain, flat.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		q := preview(t, base, map[string]any{"path": plain, "format": true})
		record["rows"] = q.summary()
		record["rows_file_bytes"] = flat.Len()
		if !q.Formatted || q.Text != want.String() {
			t.Errorf("ASSERTION: 900 KB of ordinary JSON came back formatted %v with %d bytes, want laid out with a two-space indent, %d bytes", q.Formatted, len(q.Text), want.Len())
		}
		record["daemon_peak_kb"] = daemonPeakKB(t, base)
		saveTransferArtifact(t, "file-preview-crafted-json", record)
	})

	t.Run("names", func(t *testing.T) {
		base := daemon(t)
		record := map[string]any{}
		// byte 4093 of the last short name falls inside a three-byte rune.
		short := []string{
			"src/a/b/c.txt",
			strings.Repeat("x", 4096),
			strings.Repeat("y", 4097),
			"ab" + strings.Repeat("€", 2000),
		}
		long := make([]string, 100)
		for i := range long {
			long[i] = fmt.Sprintf("long/%03d-", i) + strings.Repeat("n", 900<<10)
		}
		all := append(append([]string{}, short...), long...)
		var tgz bytes.Buffer
		gz := gzip.NewWriter(&tgz)
		tw := tar.NewWriter(gz)
		for _, name := range all {
			if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Typeflag: tar.TypeReg}); err != nil {
				t.Fatal(err)
			}
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
		tarPath := filepath.Join(base, "names.tar.gz")
		if err := os.WriteFile(tarPath, tgz.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		record["tar_gz_bytes"] = tgz.Len()

		// cut checks one listed name against the name in the archive: a name of
		// at most 4096 bytes is listed whole, a longer one as its start, whole
		// characters only, and "...", 4096 bytes at most. It reports whether
		// the name was listed so.
		cut := func(where, got, name string) bool {
			t.Helper()
			if len(name) <= 4096 {
				if got != name {
					t.Errorf("ASSERTION: in %s a name of %d bytes is listed as %d bytes, want it whole", where, len(name), len(got))
					return false
				}
				return true
			}
			head, marked := strings.CutSuffix(got, "...")
			if len(got) > 4096 || !marked || !utf8.ValidString(got) || !strings.HasPrefix(name, head) {
				t.Errorf("ASSERTION: in %s a name of %d bytes is listed as %d bytes (ends in ...: %v, valid UTF-8: %v, its start: %v), want at most 4096 bytes of its start and ...", where, len(name), len(got), marked, utf8.ValidString(got), strings.HasPrefix(name, head))
				return false
			}
			return true
		}

		p := preview(t, base, map[string]any{"path": tarPath})
		record["tar"] = p.summary()
		if p.Kind != "archive" || len(p.Entries) != len(all) {
			t.Fatalf("ASSERTION: the tar.gz of long names is %s with %d entries, want archive with %d", p.Kind, len(p.Entries), len(all))
		}
		// One of the hundred long names is enough to report.
		for i, e := range p.Entries {
			if !cut("the tar.gz", e.Name, all[i]) && i >= len(short) {
				break
			}
		}

		zipNames := []string{"src/a/b/c.txt", strings.Repeat("z", 5000)}
		zipPath := filepath.Join(base, "names.zip")
		if err := os.WriteFile(zipPath, smallZip(t, nil, false, zipNames...), 0o644); err != nil {
			t.Fatal(err)
		}
		q := preview(t, base, map[string]any{"path": zipPath})
		record["zip"] = q.summary()
		if q.Kind != "archive" || len(q.Entries) != len(zipNames) {
			t.Fatalf("ASSERTION: the zip of a long name is %s with %d entries, want archive with %d", q.Kind, len(q.Entries), len(zipNames))
		}
		for i, e := range q.Entries {
			_ = cut("the zip", e.Name, zipNames[i])
		}
		record["daemon_peak_kb"] = daemonPeakKB(t, base)
		saveTransferArtifact(t, "file-preview-crafted-names", record)
	})
}

// TestFilePreviewOpensOnlyFilmContainers previews a file named .mp4 that is an
// MPEG transport stream. ffmpeg and ffprobe are held to the listed film
// containers, so it gets no frame and no length. A playlist or concat list
// named .mp4, whose demuxers open other files, is refused the same way. The
// positive half is TestFilePreviewOnAHost's real MP4.
func TestFilePreviewOpensOnlyFilmContainers(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("no ffmpeg here")
	}
	base := t.TempDir()
	killDaemon(t, base)
	if out, err := tuiosCLI(t, base, "new", "home", "--detach"); err != nil {
		t.Fatalf("start the daemon: %v\n%s", err, out)
	}
	ts := filepath.Join(base, "not-really.mp4")
	if out, err := exec.Command(ff, "-v", "error", "-f", "lavfi", "-i", "testsrc=duration=2:size=320x240:rate=10", "-g", "5", "-f", "mpegts", ts).CombinedOutput(); err != nil {
		t.Fatalf("make the transport stream: %v\n%s", err, out)
	}
	var v filePreview
	dialFileVerbs(t, base).must("file-preview", map[string]any{"path": ts, "max_px": 160}, &v)
	saveTransferArtifact(t, "file-preview-containers", v)
	if v.Image != "" || v.DurationMs != 0 {
		t.Errorf("ASSERTION: a transport stream named .mp4 was opened: frame %d bytes, length %d ms", len(v.Image), v.DurationMs)
	}
}
