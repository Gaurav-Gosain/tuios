//go:build darwin

package preview

import (
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestNativeDecoders draws PDF pages with Quartz and decodes pictures with
// ImageIO, and writes each to PREVIEW_OUT as a PNG for a person to look at.
// PREVIEW_FILES holds the inputs. It skips when either is unset.
func TestNativeDecoders(t *testing.T) {
	in, out := os.Getenv("PREVIEW_FILES"), os.Getenv("PREVIEW_OUT")
	if in == "" || out == "" {
		t.Skip("PREVIEW_FILES and PREVIEW_OUT are not set")
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	save := func(name string, img image.Image) {
		f, err := os.Create(filepath.Join(out, name))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
	}
	pdf := func(file string, page, px int, name string) {
		path := filepath.Join(in, file)
		if _, err := os.Stat(path); err != nil {
			t.Skipf("%s is missing", path)
		}
		start := time.Now()
		d, err := nativePDF(path)
		if err != nil {
			t.Fatalf("open %s: %v", file, err)
		}
		defer d.Close()
		opened := time.Since(start)
		w, h := d.PageSize(page)
		start = time.Now()
		img, err := d.Render(ctx, page, px)
		if err != nil {
			t.Fatalf("render %s page %d: %v", file, page+1, err)
		}
		drawn := time.Since(start)
		b := img.Bounds()
		t.Logf("%s: %d pages (%s), open %v; page %d %.0fx%.0f pt drawn at %dx%d in %v", file, d.Pages(), d.Engine(), opened, page+1, w, h, b.Dx(), b.Dy(), drawn)
		save(name, img)
	}
	pdf("report.pdf", 0, 1200, "quartz-report-p1.png")
	pdf("report.pdf", 2, 1200, "quartz-report-p3.png")
	pdf("book.pdf", 199, 1200, "quartz-book-p200.png")

	pic := func(file string, maxPx int, name string) {
		path := filepath.Join(in, file)
		if _, err := os.Stat(path); err != nil {
			t.Skipf("%s is missing", path)
		}
		start := time.Now()
		img, w, h, err := nativeImage(ctx, path, maxPx)
		if err != nil {
			t.Fatalf("decode %s: %v", file, err)
		}
		b := img.Bounds()
		t.Logf("%s: %dx%d, decoded at %dx%d in %v", file, w, h, b.Dx(), b.Dy(), time.Since(start))
		save(name, img)
	}
	pic("shot.heic", 1600, "imageio-shot-heic.png")
	pic("photo.jpg", 1600, "imageio-photo-jpg.png")
	pic("banner.webp", 1600, "imageio-banner-webp.png")

	// A JPEG stored on its side, with EXIF orientation 6: it comes back
	// upright, 1200 by 800, as the banner it was made from.
	if path := filepath.Join(in, "rot6.jpg"); fileExists(path) {
		img, w, h, err := nativeImage(ctx, path, 1600)
		if err != nil {
			t.Fatalf("decode rot6.jpg: %v", err)
		}
		if b := img.Bounds(); w != 1200 || h != 800 || b.Dx() != 1200 || b.Dy() != 800 {
			t.Fatalf("rot6.jpg: size %dx%d, picture %dx%d, want 1200x800 upright", w, h, b.Dx(), b.Dy())
		}
		save("imageio-rot6-upright.png", img)
	}

	// AVFoundation: a film's facts and frames, a sound file's tags and
	// cover.
	media := func(file string, atMs int64, name string) {
		path := filepath.Join(in, file)
		if !fileExists(path) {
			t.Logf("%s is missing", path)
			return
		}
		start := time.Now()
		f, err := avFacts(ctx, path)
		if err != nil {
			t.Fatalf("facts %s: %v", file, err)
		}
		t.Logf("%s facts in %v: %+v", file, time.Since(start), f)
		start = time.Now()
		img, err := avFrame(ctx, path, atMs, 1280)
		if err != nil {
			t.Fatalf("frame %s: %v", file, err)
		}
		b := img.Bounds()
		t.Logf("%s frame at %d ms: %dx%d in %v", file, atMs, b.Dx(), b.Dy(), time.Since(start))
		save(name, img)
	}
	media("film.mp4", 30000, "avf-film-30s.png")
	media("film.mp4", 59900, "avf-film-end.png")
	media("song.mp3", 0, "avf-song-cover.png")

	// The package's own entry points pick the native path on this system.
	start := time.Now()
	p, err := DecodeImage(ctx, filepath.Join(in, "shot.heic"), 1600)
	if err != nil {
		t.Fatalf("DecodeImage heic: %v", err)
	}
	t.Logf("DecodeImage(shot.heic): format %s, %dx%d, in %v", p.Format, p.W, p.H, time.Since(start))
	start = time.Now()
	d, err := OpenPDF(ctx, filepath.Join(in, "book.pdf"))
	if err != nil {
		t.Fatalf("OpenPDF: %v", err)
	}
	t.Logf("OpenPDF(book.pdf): engine %s, %d pages, in %v", d.Engine(), d.Pages(), time.Since(start))
	d.Close()
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
