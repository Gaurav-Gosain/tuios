package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Gaurav-Gosain/mygo/ui"

	"github.com/Gaurav-Gosain/tuios/internal/guios/proto"
)

// favicons are the icons browser panes show in their address bars. An
// icon shows from memory as soon as its site's page does, else from the
// disk, where the last icon of each site is kept for the next launch;
// the page then tells which icon it has, which is downloaded only when it
// is another, or old.
type favicons struct {
	// icons are the decoded icons, by their URL; sites are the URL of each
	// site's last icon, by origin.
	icons map[string]*favicon
	sites map[string]string
	// reading are the sites whose icon is being read from the disk, and
	// fetching the icons being downloaded.
	reading, fetching map[string]bool
}

type favicon struct {
	img  ui.ImageSource
	data []byte
	at   time.Time
}

// faviconMaxAge is how long an icon is kept before it is downloaded again.
const faviconMaxAge = 7 * 24 * time.Hour

// faviconDir is where sites' icons are kept: the user's cache directory,
// as they can always be downloaded again, else kero's.
func faviconDir() string {
	if os.Getenv("GUIOS_HOME") == "" {
		if base, err := os.UserCacheDir(); err == nil {
			return filepath.Join(base, proto.Name(), "favicons")
		}
	}
	return filepath.Join(proto.Dir(), "favicons")
}

// siteOrigin returns the scheme and host of a page's URL: what its icon
// is kept by.
func siteOrigin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// favicon returns the icon of a browser pane's page: the one it told,
// else its site's last, which it reads from the disk the first time.
func (a *App) favicon(e *webEntry) ui.ImageSource {
	f := &a.icons
	if f.icons == nil {
		f.icons, f.sites = map[string]*favicon{}, map[string]string{}
		f.reading, f.fetching = map[string]bool{}, map[string]bool{}
	}
	if i := f.icons[e.iconURL]; e.iconURL != "" && i != nil {
		return i.img
	}
	origin := siteOrigin(e.url)
	if origin == "" {
		return nil
	}
	if u, ok := f.sites[origin]; ok {
		if i := f.icons[u]; i != nil {
			return i.img
		}
		return nil
	}
	if !f.reading[origin] {
		f.reading[origin] = true
		go func() {
			u, data, img, at := readFavicon(origin)
			a.update(func() {
				delete(f.reading, origin)
				if _, ok := f.sites[origin]; ok {
					return // the page told its icon meanwhile
				}
				f.sites[origin] = u
				if img != nil && f.icons[u] == nil {
					f.icons[u] = &favicon{img, data, at}
				}
			})
		}()
	}
	return nil
}

// findFavicon asks the page which icons it has, best first, and shows the
// first that decodes, downloading it unless it is in memory and fresh.
func (a *App) findFavicon(e *webEntry) {
	if e.view == nil || e.view.IsDestroyed() || siteOrigin(e.url) == "" {
		return
	}
	page, at := e.view.Page(), e.url
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		v, err := page.EvalContext(ctx, faviconScript)
		if err != nil {
			return
		}
		list, _ := v.([]any)
		var urls []string
		for _, u := range list {
			if s, ok := u.(string); ok && s != "" {
				urls = append(urls, s)
			}
		}
		if len(urls) == 0 {
			return
		}
		a.update(func() {
			if e.url != at {
				return // the page went elsewhere
			}
			a.useFavicon(e, urls)
		})
	}()
}

func (a *App) useFavicon(e *webEntry, urls []string) {
	f := &a.icons
	a.favicon(e) // makes the maps
	origin := siteOrigin(e.url)
	for _, u := range urls {
		if i := f.icons[u]; i != nil && time.Since(i.at) < faviconMaxAge {
			e.iconURL = u
			if f.sites[origin] != u {
				f.sites[origin] = u
				go writeFavicon(origin, u, i.data)
			}
			return
		}
	}
	key := strings.Join(urls, "\n")
	if f.fetching[key] {
		return
	}
	f.fetching[key] = true
	go func() {
		u, data, img := fetchFavicon(urls)
		if img != nil {
			writeFavicon(origin, u, data)
		}
		a.update(func() {
			// Icons that fail stay unasked for until the next launch.
			if img == nil {
				return
			}
			delete(f.fetching, key)
			f.icons[u] = &favicon{img, data, time.Now()}
			f.sites[origin] = u
			if siteOrigin(e.url) == origin {
				e.iconURL = u
			}
		})
	}()
}

// faviconScript lists the page's icons, best first for a small one on a
// Retina screen: an icon of 32 pixels or more, the smallest such, then
// one of no size said, an SVG, a smaller one, Apple's touch icon, and
// the site's /favicon.ico.
const faviconScript = `
const out = [];
const links = [...document.querySelectorAll('link[rel~="icon" i], link[rel~="apple-touch-icon" i], link[rel~="apple-touch-icon-precomposed" i]')]
	.filter(l => l.href && !(l.media && !matchMedia(l.media).matches));
const size = l => {
	let best = 0;
	for (const s of (l.getAttribute('sizes') || '').toLowerCase().split(/\s+/)) {
		if (s === 'any') return 1e4;
		const n = parseInt(s, 10);
		if (n > best) best = n;
	}
	return best;
};
const rank = l => {
	const touch = /apple-touch-icon/i.test(l.rel);
	const svg = /svg/i.test(l.type) || /\.svg(\?|#|$)/i.test(l.href);
	const n = size(l);
	if (touch) return [5, n];
	if (n >= 32 && n < 1e4) return [0, n];
	if (n === 0 && !svg) return [1, 0];
	if (svg) return [2, 0];
	return [3, -n];
};
links.map(l => [rank(l), l.href]).sort((a, b) => a[0][0] - b[0][0] || a[0][1] - b[0][1]).forEach(x => out.push(x[1]));
if (location.protocol.startsWith('http')) out.push(location.origin + '/favicon.ico');
return [...new Set(out)];
`

var faviconClient = &http.Client{Timeout: 10 * time.Second}

// fetchFavicon downloads the first of urls that decodes, and returns its
// URL, its bytes and its image.
func fetchFavicon(urls []string) (string, []byte, ui.ImageSource) {
	for _, u := range urls {
		data, err := getFavicon(u)
		if err != nil {
			continue
		}
		if img := decodeFavicon(data); img != nil {
			return u, data, img
		}
	}
	return "", nil, nil
}

func getFavicon(u string) ([]byte, error) {
	if rest, ok := strings.CutPrefix(u, "data:"); ok {
		meta, payload, ok := strings.Cut(rest, ",")
		if !ok {
			return nil, errors.New("bad data URL")
		}
		if strings.HasSuffix(meta, ";base64") {
			return base64.StdEncoding.DecodeString(payload)
		}
		s, err := url.PathUnescape(payload)
		return []byte(s), err
	}
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) "+browserUA())
	req.Header.Set("Accept", "image/avif,image/webp,image/png,image/svg+xml,image/*;q=0.8,*/*;q=0.5")
	res, err := faviconClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, errors.New(res.Status)
	}
	return io.ReadAll(io.LimitReader(res.Body, 1<<20))
}

// decodeFavicon decodes an icon: an ICO, an SVG, or what a Bitmap
// decodes.
func decodeFavicon(data []byte) ui.ImageSource {
	if img := decodeICO(data); img != nil {
		return ui.NewBitmap(img)
	}
	head := bytes.TrimSpace(data[:min(len(data), 512)])
	if bytes.HasPrefix(head, []byte("<")) && bytes.Contains(data, []byte("<svg")) {
		if s, err := ui.ParseSVG(data); err == nil {
			return s
		}
		return nil
	}
	if b, err := ui.DecodeBitmap(data); err == nil {
		return b
	}
	return nil
}

// decodeICO decodes the picture of an ICO file nearest 32 pixels and no
// smaller, else its largest: a PNG, or a bitmap of 32, 24 or 8 bits.
func decodeICO(data []byte) image.Image {
	if len(data) < 6 || binary.LittleEndian.Uint16(data[0:]) != 0 || binary.LittleEndian.Uint16(data[2:]) != 1 {
		return nil
	}
	n := int(binary.LittleEndian.Uint16(data[4:]))
	best, bestSize := -1, 0
	better := func(s, than int) bool {
		if than == 0 {
			return true
		}
		if s >= 32 && than >= 32 {
			return s < than
		}
		return s > than
	}
	for i := range n {
		at := 6 + 16*i
		if at+16 > len(data) {
			return nil
		}
		s := int(data[at])
		if s == 0 {
			s = 256
		}
		if better(s, bestSize) {
			best, bestSize = i, s
		}
	}
	if best < 0 {
		return nil
	}
	at := 6 + 16*best
	size := int(binary.LittleEndian.Uint32(data[at+8:]))
	off := int(binary.LittleEndian.Uint32(data[at+12:]))
	if off < 0 || size <= 0 || off+size > len(data) {
		return nil
	}
	pic := data[off : off+size]
	if bytes.HasPrefix(pic, []byte("\x89PNG")) {
		img, _, err := image.Decode(bytes.NewReader(pic))
		if err != nil {
			return nil
		}
		return img
	}
	return decodeDIB(pic)
}

// decodeDIB decodes an ICO's bitmap: its header, its pixels bottom up,
// then a mask of a bit a pixel, set where the pixel is clear.
func decodeDIB(d []byte) image.Image {
	if len(d) < 40 {
		return nil
	}
	hdr := int(binary.LittleEndian.Uint32(d[0:]))
	w := int(int32(binary.LittleEndian.Uint32(d[4:])))
	h := int(int32(binary.LittleEndian.Uint32(d[8:]))) / 2
	bpp := int(binary.LittleEndian.Uint16(d[14:]))
	colors := int(binary.LittleEndian.Uint32(d[32:]))
	if w <= 0 || h <= 0 || w > 512 || h > 512 || hdr < 40 || hdr > len(d) {
		return nil
	}
	var palette []color.NRGBA
	at := hdr
	if bpp <= 8 {
		if colors == 0 {
			colors = 1 << bpp
		}
		if at+4*colors > len(d) {
			return nil
		}
		for i := range colors {
			p := d[at+4*i:]
			palette = append(palette, color.NRGBA{p[2], p[1], p[0], 255})
		}
		at += 4 * colors
	}
	stride := (w*bpp + 31) / 32 * 4
	maskStride := (w + 31) / 32 * 4
	if bpp != 32 && bpp != 24 && bpp != 8 && bpp != 4 && bpp != 1 {
		return nil
	}
	if at+stride*h > len(d) {
		return nil
	}
	mask := at + stride*h
	hasMask := mask+maskStride*h <= len(d)
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	alpha := false
	for y := range h {
		row := d[at+(h-1-y)*stride:]
		for x := range w {
			var c color.NRGBA
			switch bpp {
			case 32:
				c = color.NRGBA{row[4*x+2], row[4*x+1], row[4*x], row[4*x+3]}
				alpha = alpha || c.A != 0
			case 24:
				c = color.NRGBA{row[3*x+2], row[3*x+1], row[3*x], 255}
			default:
				bit := x * bpp
				i := int(row[bit/8]>>(8-bpp-bit%8)) & (1<<bpp - 1)
				if i < len(palette) {
					c = palette[i]
				}
			}
			img.SetNRGBA(x, y, c)
		}
	}
	// A bitmap of 32 bits has its alpha, unless every pixel's is nought:
	// the mask then says.
	if (bpp != 32 || !alpha) && hasMask {
		for y := range h {
			row := d[mask+(h-1-y)*maskStride:]
			for x := range w {
				if row[x/8]>>(7-x%8)&1 == 1 {
					img.Pix[img.PixOffset(x, y)+3] = 0
				} else if bpp == 32 {
					img.Pix[img.PixOffset(x, y)+3] = 255
				}
			}
		}
	}
	return img
}

// A site's icon on the disk is a file named for its origin: the icon's
// URL on a line, then its bytes.
func faviconPath(origin string) string {
	h := sha256.Sum256([]byte(origin))
	return filepath.Join(faviconDir(), hex.EncodeToString(h[:12]))
}

func readFavicon(origin string) (string, []byte, ui.ImageSource, time.Time) {
	p := faviconPath(origin)
	data, err := os.ReadFile(p)
	if err != nil {
		return "", nil, nil, time.Time{}
	}
	u, body, ok := bytes.Cut(data, []byte("\n"))
	if !ok {
		return "", nil, nil, time.Time{}
	}
	var at time.Time
	if st, err := os.Stat(p); err == nil {
		at = st.ModTime()
	}
	return string(u), body, decodeFavicon(body), at
}

func writeFavicon(origin, u string, data []byte) {
	if u == "" || strings.Contains(u, "\n") {
		return
	}
	// A cache: an icon not written is fetched again next time.
	if err := writePrivate(faviconPath(origin), append([]byte(u+"\n"), data...)); err != nil {
		log.Printf("guios: favicon: %v", err)
	}
}
