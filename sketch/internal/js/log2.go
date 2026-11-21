package js

import "math"

const (
	ivln2hi = 1.44269504072144627571e+00
	ivln2lo = 1.67517131648865118353e-10
)

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
	if hx < 0x00100000 {
		if (hx&0x7fffffff)|int32(lx) == 0 {
			return math.Inf(-1)
		}
		if hx < 0 {
			return math.NaN()
		}
		k -= 54
		x = float64(x * two54)
		hx, _ = words(x)
	}
	if hx >= 0x7ff00000 {
		return x + x
	}
	if hx == 0x3ff00000 && lx == 0 {
		return 0
	}
	k += (hx >> 20) - 1023
	hx &= 0x000fffff
	i := (hx + 0x95f64) & 0x100000
	_, lo32 := words(x)
	x = fromWords(hx|(i^0x3ff00000), lo32)
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

	w := y + valHi
	valLo += (y - w) + valHi
	valHi = w

	return valLo + valHi
}
