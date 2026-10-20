package js

import (
	"math"
	"strconv"
)

// Log and Log10 are Math.log and Math.log10 as V8 computes them: fdlibm's e_log.c and
// e_log10.c, ported from V8's src/base/ieee754.cc. Go's math.Log10 is math.Log(x) times
// 1/ln 10, which differs in the last bit for many inputs. Products are converted to
// float64 so that the compiler does not fuse them into FMA instructions.

const (
	ln2Hi     = 6.93147180369123816490e-01 // 3fe62e42 fee00000
	ln2Lo     = 1.90821492927058770002e-10 // 3dea39ef 35793c76
	two54     = 1.80143985094819840000e+16 // 43500000 00000000
	lg1       = 6.666666666666735130e-01   // 3FE55555 55555593
	lg2       = 3.999999999940941908e-01   // 3FD99999 9997FA04
	lg3       = 2.857142874366239149e-01   // 3FD24924 94229359
	lg4       = 2.222219843214978396e-01   // 3FCC71C5 1D8E78AF
	lg5       = 1.818357216161805012e-01   // 3FC74664 96CB03DE
	lg6       = 1.531383769920937332e-01   // 3FC39A09 D078C69F
	lg7       = 1.479819860511658591e-01   // 3FC2F112 DF3E5244
	ivln10    = 4.34294481903251816668e-01
	log10_2hi = 3.01029995663611771306e-01 // 3FD34413 509F6000
	log10_2lo = 3.69423907715893078616e-13 // 3D59FEF3 11F12B36
)

func words(x float64) (hx int32, lx uint32) {
	b := math.Float64bits(x)
	return int32(b >> 32), uint32(b)
}

func fromWords(hx int32, lx uint32) float64 {
	return math.Float64frombits(uint64(uint32(hx))<<32 | uint64(lx))
}

// Log is Math.log.
func Log(x float64) float64 {
	hx, lx := words(x)
	k := int32(0)
	if hx < 0x00100000 { // x < 2**-1022
		if (hx&0x7fffffff)|int32(lx) == 0 {
			return math.Inf(-1) // log(+-0) = -inf
		}
		if hx < 0 {
			return math.NaN() // log(-#) = NaN
		}
		k -= 54
		x = float64(x * two54) // subnormal number, scale up x
		hx, _ = words(x)
	}
	if hx >= 0x7ff00000 {
		return x + x
	}
	k += (hx >> 20) - 1023
	hx &= 0x000fffff
	i := (hx + 0x95f64) & 0x100000
	_, lx = words(x)
	x = fromWords(hx|(i^0x3ff00000), lx) // normalize x or x/2
	k += i >> 20
	f := x - 1.0
	if (0x000fffff & (2 + hx)) < 3 { // -2**-20 <= f < 2**-20
		if f == 0 {
			if k == 0 {
				return 0
			}
			dk := float64(k)
			return float64(dk*ln2Hi) + float64(dk*ln2Lo)
		}
		R := float64(float64(f*f) * (0.5 - float64(0.33333333333333333*f)))
		if k == 0 {
			return f - R
		}
		dk := float64(k)
		return float64(dk*ln2Hi) - ((R - float64(dk*ln2Lo)) - f)
	}
	s := f / (2.0 + f)
	dk := float64(k)
	z := float64(s * s)
	i = hx - 0x6147a
	w := float64(z * z)
	j := 0x6b851 - hx
	t1 := float64(w * (lg2 + float64(w*(lg4+float64(w*lg6)))))
	t2 := float64(z * (lg1 + float64(w*(lg3+float64(w*(lg5+float64(w*lg7)))))))
	i |= j
	R := t2 + t1
	if i > 0 {
		hfsq := float64(float64(0.5*f) * f)
		if k == 0 {
			return f - (hfsq - float64(s*(hfsq+R)))
		}
		return float64(dk*ln2Hi) - ((hfsq - (float64(s*(hfsq+R)) + float64(dk*ln2Lo))) - f)
	}
	if k == 0 {
		return f - float64(s*(f-R))
	}
	return float64(dk*ln2Hi) - ((float64(s*(f-R)) - float64(dk*ln2Lo)) - f)
}

// Log10 is Math.log10.
func Log10(x float64) float64 {
	hx, lx := words(x)
	k := int32(0)
	if hx < 0x00100000 { // x < 2**-1022
		if (hx&0x7fffffff)|int32(lx) == 0 {
			return math.Inf(-1) // log(+-0) = -inf
		}
		if hx < 0 {
			return math.NaN() // log(-#) = NaN
		}
		k -= 54
		x = float64(x * two54) // subnormal number, scale up x
		hx, lx = words(x)
	}
	if hx >= 0x7ff00000 {
		return x + x
	}
	if hx == 0x3ff00000 && lx == 0 {
		return 0 // log(1) = +0
	}
	k += (hx >> 20) - 1023
	i := int32(uint32(k) & 0x80000000 >> 31)
	hx = (hx & 0x000fffff) | ((0x3ff - i) << 20)
	y := float64(k + i)
	x = fromWords(hx, lx)
	z := float64(y*log10_2lo) + float64(ivln10*Log(x))
	return z + float64(y*log10_2hi)
}

// Pow10 is 10 ** k for a whole k, as node computes it on linux/amd64. V8 does not port
// pow from fdlibm: Math.pow and ** call the C library's pow, so the last bit depends on
// the platform. glibc's pow gives the double nearest to 10^k for every whole k from
// -400 to 400 but two, 23 and 210, where it gives the double above. The goldens were
// written with it. TestMathAgainstNode checks every k in that range.
func Pow10(k int) float64 {
	switch k {
	case 23:
		return 1.0000000000000001e+23
	case 210:
		return 1.0000000000000001e+210
	}
	if k >= 0 && k <= 22 {
		return math.Pow10(k) // a table of exact values
	}
	f, _ := strconv.ParseFloat("1e"+strconv.Itoa(k), 64)
	return f
}
