package plot

import (
	"math"
	"strconv"
	"strings"
)

// finite reports whether v is a usable number.
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// extent returns the smallest and largest finite values, and false when
// there is none.
func extent(vals ...[]float64) (lo, hi float64, ok bool) {
	lo, hi = math.Inf(1), math.Inf(-1)
	for _, vs := range vals {
		for _, v := range vs {
			if finite(v) {
				lo, hi = math.Min(lo, v), math.Max(hi, v)
			}
		}
	}
	return lo, hi, lo <= hi
}

// unit maps v from [lo, hi] to [0, 1], clamped. A flat range maps to 0.5.
func unit(v, lo, hi float64) float64 {
	if !finite(v) {
		return 0
	}
	// Halves first, so a range such as [-1e308, 1e308] does not overflow.
	span := hi/2 - lo/2
	if span <= 0 || !finite(span) {
		return 0.5
	}
	f := (v/2 - lo/2) / span
	return math.Max(0, math.Min(1, f))
}

// Compact formats v in at most about five cells: 412, 6.2, 0.35, 12k, 3.4M.
func Compact(v float64) string {
	if !finite(v) {
		return "-"
	}
	a := math.Abs(v)
	if a >= 1e15 || (a > 0 && a < 1e-3) {
		return strconv.FormatFloat(v, 'g', 2, 64)
	}
	for _, s := range []struct {
		from, div float64
		unit      string
	}{{1e12, 1e12, "T"}, {1e9, 1e9, "G"}, {1e6, 1e6, "M"}, {1e4, 1e3, "k"}} {
		if a >= s.from {
			return trimZero(strconv.FormatFloat(v/s.div, 'f', decimals(a/s.div), 64)) + s.unit
		}
	}
	return trimZero(strconv.FormatFloat(v, 'f', decimals(a), 64))
}

func decimals(a float64) int {
	switch {
	case a == math.Trunc(a) || a >= 100:
		return 0
	case a >= 10:
		return 1
	case a >= 0.1:
		return 2
	}
	return 3
}

func trimZero(s string) string {
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	if s == "-0" {
		return "0"
	}
	return s
}

// Percent formats a fraction as a percentage with at most one decimal:
// 0.988 is 98.8%, 1 is 100%.
func Percent(f float64) string {
	if !finite(f) {
		return "-%"
	}
	return trimZero(strconv.FormatFloat(math.Round(f*1000)/10, 'f', 1, 64)) + "%"
}

// Downsample reduces vals to at most n points with largest triangle three
// buckets, which keeps the peaks a plain average would flatten. Missing
// values are dropped first. It returns vals itself when it already fits.
func Downsample(vals []float64, n int) []float64 {
	if n <= 0 {
		return nil
	}
	clean := make([]float64, 0, len(vals))
	for _, v := range vals {
		if finite(v) {
			clean = append(clean, v)
		}
	}
	if len(clean) <= n {
		return clean
	}
	if n < 3 {
		out := make([]float64, n)
		for i := range out {
			out[i] = clean[i*(len(clean)-1)/max(n-1, 1)]
		}
		return out
	}
	out := make([]float64, 0, n)
	out = append(out, clean[0])
	bucket := float64(len(clean)-2) / float64(n-2)
	a := 0
	for i := 0; i < n-2; i++ {
		start := int(float64(i)*bucket) + 1
		end := min(int(float64(i+1)*bucket)+1, len(clean)-1)
		nstart := end
		nend := min(int(float64(i+2)*bucket)+1, len(clean))
		var avgX, avgY float64
		for j := nstart; j < nend; j++ {
			avgX += float64(j)
			avgY += clean[j]
		}
		if c := float64(nend - nstart); c > 0 {
			avgX /= c
			avgY /= c
		}
		best, bestArea := start, -1.0
		for j := start; j < end; j++ {
			area := math.Abs((float64(a)-avgX)*(clean[j]-clean[a]) - (float64(a)-float64(j))*(avgY-clean[a]))
			if area > bestArea {
				best, bestArea = j, area
			}
		}
		out = append(out, clean[best])
		a = best
	}
	return append(out, clean[len(clean)-1])
}

// buckets averages vals into n buckets, keeping missing values out. A bucket
// with no value is NaN.
func buckets(vals []float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		lo := i * len(vals) / n
		hi := (i + 1) * len(vals) / n
		sum, cnt := 0.0, 0
		for _, v := range vals[lo:max(hi, lo)] {
			if finite(v) {
				sum += v
				cnt++
			}
		}
		if cnt == 0 {
			out[i] = math.NaN()
		} else {
			out[i] = sum / float64(cnt)
		}
	}
	return out
}
