package preview

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"image"
	"strconv"
	"strings"
	"sync"
)

// Doc is a PDF document open for its pages. Its methods are safe from any
// goroutine; Render runs as long as a page takes to draw, so call it off the
// UI thread.
type Doc interface {
	// Pages is the page count.
	Pages() int
	// PageSize is page i's size in points, as it shows (its rotation
	// applied), i from 0.
	PageSize(i int) (w, h float64)
	// Render draws page i widthPx pixels wide.
	Render(ctx context.Context, i, widthPx int) (image.Image, error)
	// Words is page i's text, laid out as on the page.
	Words(ctx context.Context, i int) (string, error)
	// Engine names what draws the pages, for the viewer's details.
	Engine() string
	Close()
}

// nativePDF opens a PDF with the platform's renderer: Quartz on macOS.
var nativePDF func(path string) (Doc, error)

// MaxPagePx bounds a page's width in pixels, zoomed in as far as it goes.
const MaxPagePx = 4096

// OpenPDF opens the PDF at path: with the platform's renderer where there
// is one, else with poppler's tools.
func OpenPDF(ctx context.Context, path string) (Doc, error) {
	if nativePDF != nil {
		d, err := nativePDF(path)
		if err == nil {
			return d, nil
		}
		if Tool("pdfinfo") == "" {
			return nil, err
		}
	}
	return openPoppler(ctx, path)
}

// popplerDoc draws pages with pdftoppm, one run a page, and reads their
// sizes with one run of pdfinfo.
type popplerDoc struct {
	path  string
	pages int
	sizes [][2]float64 // by page; the last size stands for the pages past it
	mu    sync.Mutex
}

// maxSizedPages bounds the pages whose sizes pdfinfo lists; the pages past
// them take the last page's size.
const maxSizedPages = 5000

func openPoppler(ctx context.Context, path string) (Doc, error) {
	if Tool("pdftoppm") == "" {
		return nil, ErrNoTool("pdftoppm")
	}
	// One run says the count and each page's size: pdfinfo stops at the
	// last page of a shorter range.
	out, err := run(ctx, 16<<20, "pdfinfo", "-f", "1", "-l", strconv.Itoa(maxSizedPages), path)
	if err != nil {
		if _, ok := err.(ErrNoTool); ok {
			return nil, err
		}
		return nil, pdfError(out, err)
	}
	d := &popplerDoc{path: path}
	for line := range strings.SplitSeq(string(out), "\n") {
		if rest, ok := strings.CutPrefix(line, "Pages:"); ok {
			d.pages, _ = strconv.Atoi(strings.TrimSpace(rest))
		}
	}
	if d.pages <= 0 {
		return nil, ErrNoPages
	}
	d.sizes = popplerSizes(out, min(d.pages, maxSizedPages))
	if len(d.sizes) == 0 {
		d.sizes = [][2]float64{{612, 792}}
	}
	return d, nil
}

// pdfError is what the viewer says of a PDF poppler could not open.
func pdfError(out []byte, err error) error {
	s := strings.ToLower(string(out))
	if strings.Contains(s, "password") || strings.Contains(s, "encrypt") {
		return ErrLocked
	}
	return ErrUnreadable
}

// popplerSizes reads "Page N size: W x H pts" and "Page N rot: R" lines.
func popplerSizes(out []byte, n int) [][2]float64 {
	sizes := make([][2]float64, 0, n)
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 4 || f[0] != "Page" {
			continue
		}
		page, err := strconv.Atoi(f[1])
		if err != nil || page < 1 || page > n {
			continue
		}
		switch f[2] {
		case "size:":
			if len(f) < 6 {
				continue
			}
			w, _ := strconv.ParseFloat(f[3], 64)
			h, _ := strconv.ParseFloat(f[5], 64)
			for len(sizes) < page {
				sizes = append(sizes, [2]float64{w, h})
			}
			sizes[page-1] = [2]float64{w, h}
		case "rot:":
			if r, _ := strconv.Atoi(f[3]); (r/90)%2 == 1 && page <= len(sizes) {
				s := &sizes[page-1]
				s[0], s[1] = s[1], s[0]
			}
		}
	}
	return sizes
}

func (d *popplerDoc) Pages() int     { return d.pages }
func (d *popplerDoc) Engine() string { return "poppler" }
func (d *popplerDoc) Close()         {}

func (d *popplerDoc) PageSize(i int) (w, h float64) {
	s := d.sizes[min(max(i, 0), len(d.sizes)-1)]
	return s[0], s[1]
}

func (d *popplerDoc) Render(ctx context.Context, i, widthPx int) (image.Image, error) {
	widthPx = min(max(widthPx, 16), MaxPagePx)
	p := strconv.Itoa(i + 1)
	// PPM, as pdftoppm writes it with no format named: no compression to
	// make and undo, five times faster than PNG for a page.
	out, err := run(ctx, 160<<20, "pdftoppm", "-f", p, "-l", p, "-scale-to-x", strconv.Itoa(widthPx), "-scale-to-y", "-1",
		"-aa", "yes", "-aaVector", "yes", "-singlefile", d.path)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, ErrPageFailed
	}
	img, err := decodePPM(out)
	if err != nil {
		return nil, ErrPageFailed
	}
	return img, nil
}

func (d *popplerDoc) Words(ctx context.Context, i int) (string, error) {
	p := strconv.Itoa(i + 1)
	out, err := run(ctx, 1<<20, "pdftotext", "-f", p, "-l", p, "-layout", d.path, "-")
	if err != nil {
		return "", err
	}
	return strings.ToValidUTF8(string(out), ""), nil
}

// decodePPM reads a binary PPM (P6, maxval 255) into an RGBA picture.
func decodePPM(b []byte) (*image.RGBA, error) {
	var fields [4]int
	pos := 0
	field := func() (int, bool) {
		for pos < len(b) {
			switch c := b[pos]; {
			case c == '#':
				for pos < len(b) && b[pos] != '\n' {
					pos++
				}
			case c == ' ' || c == '\t' || c == '\n' || c == '\r':
				pos++
			default:
				goto num
			}
		}
		return 0, false
	num:
		start := pos
		for pos < len(b) && b[pos] >= '0' && b[pos] <= '9' {
			pos++
		}
		n, err := strconv.Atoi(string(b[start:pos]))
		return n, err == nil
	}
	if len(b) < 2 || b[0] != 'P' || b[1] != '6' {
		return nil, errors.New("not a PPM")
	}
	pos = 2
	for k := 1; k < 4; k++ {
		n, ok := field()
		if !ok {
			return nil, errors.New("short PPM header")
		}
		fields[k] = n
	}
	w, h, maxval := fields[1], fields[2], fields[3]
	pos++ // the one whitespace after maxval
	if maxval != 255 || w <= 0 || h <= 0 || len(b)-pos < w*h*3 {
		return nil, errors.New("bad PPM")
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	src := b[pos:]
	dst := img.Pix
	for p, q := 0, 0; p < w*h*3; p, q = p+3, q+4 {
		dst[q], dst[q+1], dst[q+2], dst[q+3] = src[p], src[p+1], src[p+2], 255
	}
	return img, nil
}
