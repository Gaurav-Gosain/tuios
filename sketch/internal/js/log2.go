package js

import "math"

// Log2 is Math.log2 as V8 13.6, the V8 of node 24, computes it: FreeBSD's e_log2.c from
// V8's src/base/ieee754.cc. Go's math.Log2 splits x with Frexp and adds Log(frac)/ln 2,
// which differs in the last bit for some inputs. Products are converted to float64 so
// that the compiler does not fuse them into FMA instructions.

const (
	ivln2hi = 1.44269504072144627571e+00 // 0x3FF71547, 0x65200000
	ivln2lo = 1.67517131648865118353e-10 // 0x3DE705FC, 0x2EEFA200
)

// kLog1p is k_log1p(f): log(1+f) - f + f*f/2, for 1+f in [sqrt(2)/2, sqrt(2)].
func kLog1p(f float64) float64 {
	s := f / (2.0 + f)
	z := float64(s * s)
	w := float64(z * z)
	t1 := float64(w * (lg2 + float64(w*(lg4+float64(w*lg6)))))
	t2 := float64(z * (lg1 + float64(w*(lg3+float64(w*(lg5+float64(w*lg7)))))))
	R := t2 + t1
	hfsq := float64(float64(0.5*f) * f)
	return float64(s * (hfsq + R))
}

// Log2 is Math.log2.
func Log2(x float64) float64 {
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
	if hx == 0x3ff00000 && lx == 0 {
		return 0 // log(1) = +0
	}
	k += (hx >> 20) - 1023
	hx &= 0x000fffff
	i := (hx + 0x95f64) & 0x100000
	_, lo32 := words(x)
	x = fromWords(hx|(i^0x3ff00000), lo32) // normalize x or x/2
	k += i >> 20
	y := float64(k)
	f := x - 1.0
	hfsq := float64(float64(0.5*f) * f)
	r := kLog1p(f)

	hi := f - hfsq
	hiWord, _ := words(hi)
	hi = fromWords(hiWord, 0)
	lo := (f - hi) - hfsq + r
	valHi := float64(hi * ivln2hi)
	valLo := float64((lo+hi)*ivln2lo) + float64(lo*ivln2hi)

	// spadd(val_hi, val_lo, y), except for not using double_t:
	w := y + valHi
	valLo += (y - w) + valHi
	valHi = w

	return valLo + valHi
}
