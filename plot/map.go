package plot

import (
	"bytes"
	"compress/flate"
	_ "embed" // the world map data
	"encoding/binary"
	"io"
	"math"
	"strings"
	"sync"
)

//go:generate go run ./internal/worldgen -o world.bin

// worldBin is the land and the countries of the world, as worldgen writes
// them: Natural Earth 1:50m Admin 0 Countries, version 5.1.1, from
// https://naciscdn.org/naturalearth/50m/cultural/ne_50m_admin_0_countries.zip.
// Natural Earth is in the public domain
// (https://www.naturalearthdata.com/about/terms-of-use/). It is 720 by 288
// cells of half a degree, from 180 W to 180 E and from 84 N to 60 S.
//
//go:embed world.bin
var worldBin string

// world is worldBin decoded.
type world struct {
	w, h        int
	top, bottom float64
	codes       []string
	cells       []uint8 // 0 sea, 1 land with no code, k+2 for codes[k]
	small       map[int]uint8
	smallBits   []uint64         // the cells in small, so a lookup is a bit test
	index       map[string]uint8 // code to its cell value
}

var (
	worldOnce sync.Once
	worldMap  *world
)

// loadWorld decodes the map on first use, so a program that draws no map
// spends nothing on it.
func loadWorld() *world {
	worldOnce.Do(func() {
		raw, err := io.ReadAll(flate.NewReader(strings.NewReader(worldBin)))
		if err != nil {
			panic("plot: world.bin: " + err.Error())
		}
		worldMap = decodeWorld(raw)
	})
	return worldMap
}

func decodeWorld(raw []byte) *world {
	bad := func() { panic("plot: world.bin is damaged") }
	if len(raw) < 13 || string(raw[:4]) != "PLM1" {
		bad()
	}
	var hd [4]int16
	if err := binary.Read(bytes.NewReader(raw[4:12]), binary.LittleEndian, &hd); err != nil {
		bad()
	}
	wd := &world{w: int(hd[0]), h: int(hd[1]), top: float64(hd[2]), bottom: float64(hd[3]),
		small: map[int]uint8{}, index: map[string]uint8{}}
	n := int(raw[12])
	p := 13
	if len(raw) < p+2*n {
		bad()
	}
	for k := 0; k < n; k++ {
		c := string(raw[p : p+2])
		wd.codes = append(wd.codes, c)
		wd.index[c] = uint8(k + 2)
		p += 2
	}
	size := wd.w * wd.h
	bits := raw[p:min(p+(size+7)/8, len(raw))]
	p += len(bits)
	if len(bits) != (size+7)/8 {
		bad()
	}
	wd.cells = make([]uint8, size)
	var run uint64
	var id uint8
	next := func() uint64 {
		v, k := binary.Uvarint(raw[p:])
		if k <= 0 {
			bad()
		}
		p += k
		return v
	}
	for i := 0; i < size; i++ {
		if bits[i/8]&(1<<(i%8)) == 0 {
			continue
		}
		if run == 0 {
			run = next()
			if p >= len(raw) {
				bad()
			}
			id = raw[p]
			p++
		}
		wd.cells[i] = id + 1
		run--
	}
	cell := 0
	for k := next(); k > 0; k-- {
		cell += int(next())
		if p >= len(raw) {
			bad()
		}
		wd.small[cell] = raw[p] + 1
		p++
	}
	wd.smallBits = make([]uint64, (size+63)/64)
	for c := range wd.small {
		if c < 0 || c >= size {
			bad()
		}
		wd.smallBits[c/64] |= 1 << (c % 64)
	}
	return wd
}

// Map is a dot-matrix world map: land in the Land role, each country with
// a value filled with a shade of Ramp by its value (a choropleth), and
// optional markers. The projection is equirectangular, from 180 W to 180 E
// and from 84 N to 60 S, so Antarctica is left out.
//
// The map keeps its shape and is centred in the grid. A cell is about
// twice as tall as it is wide, as Aspect sets.
//
// The idea and the look come from iptop (github.com/franlol/iptop, MIT),
// a network monitor by franlol. The land and the borders are Natural Earth
// 1:50m Admin 0 Countries, version 5.1.1, which is in the public domain
// (https://www.naturalearthdata.com/about/terms-of-use/), rasterised by
// internal/worldgen into 720 by 288 cells of half a degree.
// A country too small to cover a third of a half-degree cell takes the one
// cell it covers most, and shows only when it has a value.
type Map struct {
	// Values are by ISO 3166-1 alpha-2 code, such as "DE" or "jp". A
	// country with no value, NaN or an unknown code shows as plain land.
	Values map[string]float64
	// Min and Max fix the range of the values. Both 0 means from 0 to the
	// largest value.
	Min, Max float64
	// Log shades by the logarithm of the value, so values that span
	// orders of magnitude, such as traffic, spread over the shades. With
	// no Min and Max the range is then from the smallest value above zero
	// to the largest.
	Log bool
	// Ramp is the shades, low to high, at most 64. Empty means Heat1 to
	// Heat5: with Land, six tones.
	Ramp []Role
	// Land colours the land of the countries with no value. None means
	// Track.
	Land    Role
	Markers []Marker
	// Aspect is a cell's height over its width. 0 means 2.2, a common
	// terminal font.
	Aspect float64
	// Stretch fills the whole grid instead of keeping the map's shape.
	Stretch bool
	// Format writes the values of the legend. Nil means Compact.
	Format func(float64) string
}

// Marker is a point on the map, such as a host. It lights the dot at its
// place in its role, over any land or shade. In ASCII it is an &.
type Marker struct {
	Lat, Lon float64
	Role     Role // Fg when empty
}

var mapRamp = []Role{Heat1, Heat2, Heat3, Heat4, Heat5}

func (mp Map) ramp() []Role {
	if len(mp.Ramp) > 0 {
		return mp.Ramp[:min(len(mp.Ramp), 64)]
	}
	return mapRamp
}

func (mp Map) land() Role {
	if mp.Land != None {
		return mp.Land
	}
	return Track
}

// rng returns the range of the values, and false when there is none.
func (mp Map) rng() (lo, hi float64, ok bool) {
	if (mp.Min != 0 || mp.Max != 0) && finite(mp.Min) && finite(mp.Max) && mp.Max > mp.Min {
		return mp.Min, mp.Max, true
	}
	lo, hi, ok = math.Inf(1), math.Inf(-1), false
	for _, v := range mp.Values {
		if finite(v) {
			hi, ok = math.Max(hi, v), true
			if v > 0 {
				lo = math.Min(lo, v)
			}
		}
	}
	switch {
	case !ok:
		return 0, 0, false
	case hi <= 0:
		return 0, 1, true
	case mp.Log && lo < hi:
		return lo, hi, true
	}
	return 0, hi, true
}

// valueAt returns the value at position t of the range, from 0 to 1, on
// the map's scale.
func (mp Map) valueAt(t, lo, hi float64) float64 {
	if mp.Log && lo > 0 {
		return lo * math.Exp(t*math.Log(hi/lo))
	}
	if mp.Log {
		return lo + math.Expm1(t*math.Log1p(math.Max(hi-lo, 0)))
	}
	return lo + t*(hi-lo)
}

// bounds returns the lowest value of each shade: the bottom of the range,
// then the range split evenly on the map's scale, each rounded to two
// digits so the legend reads 2.8k and not 2786. When rounding would merge
// two bounds, they stay as they are.
func (mp Map) bounds(lo, hi float64, n int) []float64 {
	out := make([]float64, n)
	exact := make([]float64, n)
	for k := range out {
		exact[k] = mp.valueAt(float64(k)/float64(n), lo, hi)
		out[k] = round2(exact[k])
		if k == 0 {
			out[k] = lo
		}
		if k > 0 && out[k] <= out[k-1] {
			return exact
		}
	}
	return out
}

// Legend returns one item per shade, low to high, each labelled with the
// lowest value it shows, such as "≥ 12k". The first shade is labelled with
// the bottom of the range.
func (mp Map) Legend() []LegendItem {
	lo, hi, ok := mp.rng()
	if !ok {
		return nil
	}
	f := mp.Format
	if f == nil {
		f = Compact
	}
	ramp := mp.ramp()
	out := make([]LegendItem, len(ramp))
	for k, b := range mp.bounds(lo, hi, len(ramp)) {
		out[k] = LegendItem{Label: f(b), Role: ramp[k]}
		if k > 0 {
			out[k].Label = "≥ " + out[k].Label
		}
	}
	return out
}

// round2 rounds v to two significant digits, so a legend reads 2.8k and
// not 2786.
func round2(v float64) float64 {
	if v == 0 || !finite(v) {
		return v
	}
	p := math.Pow(10, math.Floor(math.Log10(math.Abs(v)))-1)
	return math.Round(v/p) * p
}

// pixelsPerCell returns the pixels of a cell across and down: braille dots,
// half blocks, or whole cells.
func pixelsPerCell(m Mode) (int, int) {
	switch m {
	case Braille:
		return 2, 4
	case Blocks:
		return 1, 2
	}
	return 1, 1
}

// Draw draws the map into a w by h grid.
func (mp Map) Draw(w, h int, m Mode) *Grid {
	g := NewGrid(w, h)
	if g.W == 0 || g.H == 0 {
		return g
	}
	wd := loadWorld()
	px, py := pixelsPerCell(m)
	aspect := mp.Aspect
	if aspect <= 0 || !finite(aspect) || aspect > 10 {
		aspect = 2.2
	}
	// The map's size in pixels. A pixel is 1/px of a cell wide and
	// aspect/py of a cell tall, in cell widths, and the map spans 360 by
	// top-bottom degrees.
	ratio := 360 / (wd.top - wd.bottom) // width over height
	pw, ph := g.W*px, g.H*py
	if !mp.Stretch {
		want := float64(pw) / float64(px) / ratio // the height in cell widths
		if rows := want / aspect * float64(py); rows <= float64(ph) {
			ph = max(1, int(math.Round(rows)))
		} else {
			pw = max(1, int(math.Round(float64(ph)*aspect/float64(py)*ratio*float64(px))))
		}
	}
	cw, ch := (pw+px-1)/px, (ph+py-1)/py
	x0, y0 := (g.W-cw)/2, (g.H-ch)/2

	// level holds the shade of each cell value of the world, -1 for none.
	level := make([]int8, len(wd.codes)+2)
	for i := range level {
		level[i] = -1
	}
	ramp := mp.ramp()
	if lo, hi, ok := mp.rng(); ok {
		bounds := mp.bounds(lo, hi, len(ramp))
		for code, v := range mp.Values {
			id, known := wd.index[strings.ToUpper(strings.TrimSpace(code))]
			if !known || !finite(v) {
				continue
			}
			k := 0
			for k+1 < len(bounds) && v >= bounds[k+1] {
				k++
			}
			level[id] = int8(k)
		}
	}

	// pixel returns whether pixel i, j is land and its shade, -1 for
	// plain land. A pixel covers a window of the world's cells: it is land
	// when a third of them are, or when one of them is a country with a
	// shade, so a small country with a value still shows.
	pixel := func(i, j int) (bool, int) {
		sx0, sx1 := i*wd.w/pw, max((i+1)*wd.w/pw, i*wd.w/pw+1)
		sy0, sy1 := j*wd.h/ph, max((j+1)*wd.h/ph, j*wd.h/ph+1)
		landN, best := 0, -1
		for sy := sy0; sy < min(sy1, wd.h); sy++ {
			for sx := sx0; sx < min(sx1, wd.w); sx++ {
				c := sy*wd.w + sx
				v := wd.cells[c]
				if v != 0 {
					landN++
					best = max(best, int(level[v]))
				}
				if wd.smallBits[c/64]&(1<<(c%64)) != 0 {
					best = max(best, int(level[wd.small[c]]))
				}
			}
		}
		total := (min(sy1, wd.h) - sy0) * (min(sx1, wd.w) - sx0)
		return best >= 0 || landN*3 >= total && landN > 0, best
	}
	// Markers by pixel.
	marks := map[int]Role{}
	for _, mk := range mp.Markers {
		if !finite(mk.Lat) || !finite(mk.Lon) || mk.Lat > wd.top || mk.Lat < wd.bottom || mk.Lon < -180 || mk.Lon > 180 {
			continue
		}
		i := min(pw-1, int((mk.Lon+180)/360*float64(pw)))
		j := min(ph-1, int((wd.top-mk.Lat)/(wd.top-wd.bottom)*float64(ph)))
		r := mk.Role
		if r == None {
			r = Fg
		}
		marks[j*pw+i] = r
	}
	shade := func(k int) Role {
		if k < 0 {
			return mp.land()
		}
		return ramp[k]
	}

	for cy := 0; cy < ch; cy++ {
		for cx := 0; cx < cw; cx++ {
			x, y := x0+cx, y0+cy
			switch m {
			case Braille:
				var bits uint8
				best, mark := -1, None
				for dy := 0; dy < 4; dy++ {
					for dx := 0; dx < 2; dx++ {
						i, j := cx*2+dx, cy*4+dy
						if i >= pw || j >= ph {
							continue
						}
						on, k := pixel(i, j)
						if r, ok := marks[j*pw+i]; ok {
							on, mark = true, r
						}
						if on {
							bits |= brailleBit(dx, dy)
							best = max(best, k)
						}
					}
				}
				if bits == 0 {
					continue
				}
				role := shade(best)
				if mark != None {
					role = mark
				}
				g.set(x, y, string(rune(0x2800+int(bits))), role)
			case Blocks:
				var roles [2]Role
				for dy := 0; dy < 2; dy++ {
					j := cy*2 + dy
					if j >= ph {
						continue
					}
					if on, k := pixel(cx, j); on {
						roles[dy] = shade(k)
					}
					if r, ok := marks[j*pw+cx]; ok {
						roles[dy] = r
					}
				}
				switch {
				case roles[0] != None && roles[1] != None:
					g.setBg(x, y, "▀", roles[0], roles[1])
				case roles[0] != None:
					g.set(x, y, "▀", roles[0])
				case roles[1] != None:
					g.set(x, y, "▄", roles[1])
				}
			default:
				if r, ok := marks[cy*pw+cx]; ok {
					g.set(x, y, "&", r)
					continue
				}
				on, k := pixel(cx, cy)
				switch {
				case !on:
				case k < 0:
					g.set(x, y, ".", mp.land())
				default:
					// The marks DrawLegend gives the legend's items.
					g.set(x, y, asciiMarks[k%len(asciiMarks)], ramp[k])
				}
			}
		}
	}
	return g
}
