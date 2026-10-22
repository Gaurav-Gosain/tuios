// Command worldgen rasterises the Natural Earth country polygons into the
// land bitmap and the country of each land cell that plot.Map draws, and
// writes them as world.bin. go generate in tuios.dev/plot runs it.
//
// The source is Natural Earth 1:50m Admin 0 Countries, a shapefile in a
// zip from naciscdn.org, which naturalearthdata.com links to. Natural Earth
// is in the public domain. The zip's SHA-256 is pinned, so a new release
// fails the run until the pin moves on purpose.
//
// The idea and the look come from iptop by franlol
// (https://github.com/franlol/iptop, MIT). Its scripts/generate-worldmap.ts
// does the same for GeoJSON. This is a new program: it reads the shapefile,
// keeps the holes of each polygon, and samples each cell 3 by 3.
//
// The output, before compression, is:
//
//	"PLM1"
//	int16 width and height in cells, int16 top and bottom latitude
//	uint8 n, then n country codes of two bytes (ISO 3166-1 alpha-2)
//	the land bitmap: width*height bits, row-major, low bit first
//	the countries of the land cells in order, as runs: uvarint length,
//	  uint8 id (0 for a cell with no code, k for code k-1)
//	the small countries: uvarint n, then n of uvarint cell (the step from
//	  the cell before) and uint8 id. Each is a country that won no cell, at
//	  the free cell it covers most, else the nearest free cell. The map
//	  tints that cell only for a value.
//
// The file is that, compressed with DEFLATE.
package main

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
)

const (
	sourceURL    = "https://naciscdn.org/naturalearth/50m/cultural/ne_50m_admin_0_countries.zip"
	sourceSHA256 = "5fed433373581fa648920435f937d95f2d3c0200e067409c6478dcdf1b853139"
	sourceName   = "ne_50m_admin_0_countries"
)

func main() {
	src := flag.String("src", sourceURL, "the Natural Earth zip: a URL or a file")
	out := flag.String("o", "world.bin", "the file to write")
	w := flag.Int("w", 720, "cells across, from 180 W to 180 E")
	h := flag.Int("h", 288, "cells down, from -top to -bottom")
	top := flag.Int("top", 84, "the latitude of the top edge")
	bottom := flag.Int("bottom", -60, "the latitude of the bottom edge")
	flag.Parse()
	log.SetFlags(0)

	zipped, err := fetch(*src)
	if err != nil {
		log.Fatal(err)
	}
	sum := sha256.Sum256(zipped)
	if got := hex.EncodeToString(sum[:]); got != sourceSHA256 {
		log.Fatalf("%s has SHA-256 %s, want %s. Check the new release, then move the pin.", *src, got, sourceSHA256)
	}
	zr, err := zip.NewReader(bytes.NewReader(zipped), int64(len(zipped)))
	if err != nil {
		log.Fatal(err)
	}
	read := func(ext string) []byte {
		f, err := zr.Open(sourceName + ext)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		b, err := io.ReadAll(f)
		if err != nil {
			log.Fatal(err)
		}
		return b
	}
	version := strings.TrimSpace(string(read(".VERSION.txt")))
	shapes, err := readShapes(read(".shp"))
	if err != nil {
		log.Fatal(err)
	}
	records, err := readDBF(read(".dbf"))
	if err != nil {
		log.Fatal(err)
	}
	if len(records) != len(shapes) {
		log.Fatalf("%d shapes and %d records", len(shapes), len(records))
	}
	for i := range shapes {
		shapes[i].code = isoCode(records[i])
	}

	land, country, small := rasterise(shapes, *w, *h, float64(*top), float64(*bottom))
	data, codes := encode(land, country, small, *w, *h, *top, *bottom)
	var z bytes.Buffer
	zw, err := flate.NewWriter(&z, flate.BestCompression)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := zw.Write(data); err != nil {
		log.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(*out, z.Bytes(), 0o644); err != nil { //nolint:gosec // a data file the package embeds and git keeps

		log.Fatal(err)
	}
	n := 0
	for _, l := range land {
		if l {
			n++
		}
	}
	fmt.Printf("Natural Earth %s, %d shapes: %dx%d cells, %d land, %d countries (%d small); %d bytes, %d compressed, in %s\n",
		version, len(shapes), *w, *h, n, len(codes), len(small), len(data), z.Len(), *out)
}

func fetch(src string) ([]byte, error) {
	if !strings.HasPrefix(src, "https://") {
		return os.ReadFile(src)
	}
	resp, err := http.Get(src)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", src, resp.Status)
	}
	return io.ReadAll(resp.Body)
}

// shape is one country: rings of lon, lat points, and its bounding box.
type shape struct {
	rings                  [][][2]float64
	minX, minY, maxX, maxY float64
	code                   string
}

// readShapes reads the polygons of a .shp file. A null shape has no rings.
func readShapes(b []byte) ([]shape, error) {
	if len(b) < 100 || binary.BigEndian.Uint32(b) != 9994 {
		return nil, fmt.Errorf("not a shapefile")
	}
	var out []shape
	for off := 100; off+8 <= len(b); {
		n := int(binary.BigEndian.Uint32(b[off+4:])) * 2
		rec := b[off+8 : min(off+8+n, len(b))]
		off += 8 + n
		var s shape
		if len(rec) >= 44 {
			switch binary.LittleEndian.Uint32(rec) {
			case 5, 15, 25: // polygon, with z, with m
			default:
				out = append(out, s)
				continue
			}
			f := func(i int) float64 { return math.Float64frombits(binary.LittleEndian.Uint64(rec[i:])) }
			s.minX, s.minY, s.maxX, s.maxY = f(4), f(12), f(20), f(28)
			parts := int(binary.LittleEndian.Uint32(rec[36:]))
			points := int(binary.LittleEndian.Uint32(rec[40:]))
			p0 := 44 + 4*parts
			if p0+16*points > len(rec) {
				return nil, fmt.Errorf("a record is short")
			}
			for k := range parts {
				start := int(binary.LittleEndian.Uint32(rec[44+4*k:]))
				end := points
				if k+1 < parts {
					end = int(binary.LittleEndian.Uint32(rec[44+4*(k+1):]))
				}
				ring := make([][2]float64, 0, end-start)
				for i := start; i < end; i++ {
					ring = append(ring, [2]float64{f(p0 + 16*i), f(p0 + 16*i + 8)})
				}
				s.rings = append(s.rings, ring)
			}
		}
		out = append(out, s)
	}
	return out, nil
}

// readDBF reads the records of a .dbf file as field name to text.
func readDBF(b []byte) ([]map[string]string, error) {
	if len(b) < 32 {
		return nil, fmt.Errorf("not a dbf file")
	}
	count := int(binary.LittleEndian.Uint32(b[4:]))
	headLen := int(binary.LittleEndian.Uint16(b[8:]))
	recLen := int(binary.LittleEndian.Uint16(b[10:]))
	type field struct {
		name string
		n    int
	}
	var fields []field
	for off := 32; off+32 <= headLen && b[off] != 0x0d; off += 32 {
		name := strings.TrimRight(string(b[off:off+11]), "\x00")
		n := int(b[off+16])
		if b[off+11] == 'C' {
			// A text field longer than 255 keeps the high byte of its length
			// where the decimal count would be.
			n |= int(b[off+17]) << 8
		}
		fields = append(fields, field{name, n})
	}
	total := 1
	for _, f := range fields {
		total += f.n
	}
	if total != recLen {
		return nil, fmt.Errorf("the fields take %d bytes of a %d byte record", total, recLen)
	}
	out := make([]map[string]string, 0, count)
	for i := range count {
		rec := b[headLen+i*recLen:]
		if len(rec) < recLen {
			return nil, fmt.Errorf("record %d is short", i)
		}
		m := map[string]string{}
		pos := 1 // the deletion flag
		for _, f := range fields {
			// Natural Earth pads text with NUL as well as spaces.
			m[f.name] = strings.Trim(string(rec[pos:pos+f.n]), " \x00")
			pos += f.n
		}
		out = append(out, m)
	}
	return out, nil
}

// isoCode returns the ISO 3166-1 alpha-2 code of a record, or "". Natural
// Earth's ISO_A2_EH fills the codes ISO_A2 leaves as -99, such as France
// and Norway.
func isoCode(r map[string]string) string {
	for _, k := range []string{"ISO_A2_EH", "ISO_A2"} {
		if c := r[k]; len(c) == 2 && c[0] >= 'A' && c[0] <= 'Z' && c[1] >= 'A' && c[1] <= 'Z' {
			return c
		}
	}
	return ""
}

// inside reports whether lon, lat is inside the shape: inside an odd number
// of its rings, so a hole is outside.
func (s *shape) inside(x, y float64) bool {
	if x < s.minX || x > s.maxX || y < s.minY || y > s.maxY {
		return false
	}
	in := false
	for _, ring := range s.rings {
		for i, j := 0, len(ring)-1; i < len(ring); j, i = i, i+1 {
			xi, yi := ring[i][0], ring[i][1]
			xj, yj := ring[j][0], ring[j][1]
			if (yi > y) != (yj > y) && x < (xj-xi)*(y-yi)/(yj-yi)+xi {
				in = !in
			}
		}
	}
	return in
}

// sub is the samples per cell along each side, and minHits the samples
// that must be on land for the cell to be land: a third of them, so a cell
// that is mostly sea stays sea and a small island still shows.
const (
	sub     = 3
	minHits = 3
)

// rasterise returns, for each cell, whether it is land and the shape that
// covers most of it.
//
// A country that wins no cell gets one cell in small: the free cell where
// most of its samples fell, else the free cell nearest it. A country on
// the map that gets no cell stops the run.
func rasterise(shapes []shape, w, h int, top, bottom float64) (land []bool, country []string, small map[int]string) {
	land = make([]bool, w*h)
	country = make([]string, w*h)
	hits := map[int]int{}
	// cover is, per shape, its samples in each cell it touches.
	cover := make([]map[int]int, len(shapes))
	for y := range h {
		for x := range w {
			clear(hits)
			total := 0
			for sy := range sub {
				lat := top - (float64(y)+(float64(sy)+0.5)/sub)/float64(h)*(top-bottom)
				for sx := range sub {
					lon := -180 + (float64(x)+(float64(sx)+0.5)/sub)/float64(w)*360
					for i := range shapes {
						if shapes[i].inside(lon, lat) {
							hits[i]++
							total++
							break
						}
					}
				}
			}
			for i, n := range hits {
				if cover[i] == nil {
					cover[i] = map[int]int{}
				}
				cover[i][y*w+x] = n
			}
			if total < minHits {
				continue
			}
			best, bestN := -1, 0
			for i, n := range hits {
				if n > bestN || n == bestN && i < best {
					best, bestN = i, n
				}
			}
			land[y*w+x] = true
			country[y*w+x] = shapes[best].code
		}
	}
	won := map[string]bool{}
	for i, c := range country {
		if land[i] {
			won[c] = true
		}
	}
	small = map[int]string{}
	free := func(c int) bool { _, taken := small[c]; return c >= 0 && !taken }
	for i, s := range shapes {
		if s.code == "" || won[s.code] {
			continue
		}
		// The cells it covers, most samples first, then the free cell
		// nearest the best of them or the middle of its largest ring.
		cells := make([]int, 0, len(cover[i]))
		for c := range cover[i] {
			cells = append(cells, c)
		}
		sort.Slice(cells, func(a, b int) bool {
			na, nb := cover[i][cells[a]], cover[i][cells[b]]
			return na > nb || na == nb && cells[a] < cells[b]
		})
		cell := -1
		for _, c := range cells {
			if free(c) {
				cell = c
				break
			}
		}
		if cell < 0 {
			start := s.middleCell(w, h, top, bottom)
			if len(cells) > 0 {
				start = cells[0]
			}
			cell = nearestFree(start, w, h, free)
		}
		if cell < 0 {
			if s.maxY > bottom && s.minY < top {
				log.Fatalf("%s is on the map but has no cell", s.code)
			}
			fmt.Printf("%s is off the map\n", s.code)
			continue
		}
		small[cell] = s.code
		won[s.code] = true
	}
	return land, country, small
}

// nearestFree returns the free cell nearest start, in rings of growing
// size, or -1 when start is off the map or no cell within 8 is free.
func nearestFree(start, w, h int, free func(int) bool) int {
	if start < 0 {
		return -1
	}
	x0, y0 := start%w, start/w
	for r := 0; r <= 8; r++ {
		for dy := -r; dy <= r; dy++ {
			for dx := -r; dx <= r; dx++ {
				if max(abs(dx), abs(dy)) != r {
					continue
				}
				x, y := (x0+dx+w)%w, y0+dy
				if y >= 0 && y < h && free(y*w+x) {
					return y*w + x
				}
			}
		}
	}
	return -1
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// middleCell returns the cell under the middle of the shape's largest ring,
// or -1 when it is off the map.
func (s *shape) middleCell(w, h int, top, bottom float64) int {
	var best [][2]float64
	for _, r := range s.rings {
		if len(r) > len(best) {
			best = r
		}
	}
	if len(best) == 0 {
		return -1
	}
	x0, y0, x1, y1 := best[0][0], best[0][1], best[0][0], best[0][1]
	for _, p := range best {
		x0, y0 = math.Min(x0, p[0]), math.Min(y0, p[1])
		x1, y1 = math.Max(x1, p[0]), math.Max(y1, p[1])
	}
	x := int(((x0+x1)/2 + 180) / 360 * float64(w))
	y := int((top - (y0+y1)/2) / (top - bottom) * float64(h))
	if x < 0 || y < 0 || x >= w || y >= h {
		return -1
	}
	return y*w + x
}

func encode(land []bool, country []string, small map[int]string, w, h, top, bottom int) ([]byte, []string) {
	seen := map[string]bool{}
	var codes []string
	add := func(c string) {
		if c != "" && !seen[c] {
			seen[c] = true
			codes = append(codes, c)
		}
	}
	for i, l := range land {
		if l {
			add(country[i])
		}
	}
	for _, c := range small {
		add(c)
	}
	sort.Strings(codes)
	if len(codes) > 253 {
		log.Fatalf("%d countries do not fit a byte", len(codes))
	}
	id := map[string]uint8{}
	for i, c := range codes {
		id[c] = uint8(i + 1)
	}
	var b bytes.Buffer
	b.WriteString("PLM1")
	for _, v := range [4]int16{int16(w), int16(h), int16(top), int16(bottom)} {
		b.Write(binary.LittleEndian.AppendUint16(nil, uint16(v)))
	}
	b.WriteByte(uint8(len(codes)))
	for _, c := range codes {
		b.WriteString(c)
	}
	bits := make([]byte, (w*h+7)/8)
	for i, l := range land {
		if l {
			bits[i/8] |= 1 << (i % 8)
		}
	}
	b.Write(bits)
	var run uint64
	cur := -1
	flush := func() {
		if run > 0 {
			b.Write(binary.AppendUvarint(nil, run))
			b.WriteByte(uint8(cur))
		}
	}
	for i, l := range land {
		if !l {
			continue
		}
		k := int(id[country[i]])
		if k != cur {
			flush()
			cur, run = k, 0
		}
		run++
	}
	flush()
	cells := make([]int, 0, len(small))
	for c := range small {
		cells = append(cells, c)
	}
	sort.Ints(cells)
	b.Write(binary.AppendUvarint(nil, uint64(len(cells))))
	prev := 0
	for _, c := range cells {
		b.Write(binary.AppendUvarint(nil, uint64(c-prev)))
		b.WriteByte(id[small[c]])
		prev = c
	}
	return b.Bytes(), codes
}
