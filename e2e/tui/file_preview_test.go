package tuie2e

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
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
	}
	if film == "" {
		t.Logf("no ffmpeg here: the film half is skipped")
	} else {
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
