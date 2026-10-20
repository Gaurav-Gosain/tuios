package js

import "math"

// Acos is Math.acos as V8 computes it: fdlibm's e_acos.c, ported from V8's
// src/base/ieee754.cc in V8 13.6 (node 24), as Asin is. Go's math.Acos goes through
// math.Asin and differs in the last bit for many inputs. Products are converted to
// float64 so that the compiler does not fuse them into FMA instructions.

const acosPi = 3.14159265358979311600e+00 // 0x400921FB, 0x54442D18

// Acos is Math.acos.
func Acos(x float64) float64 {
	hx, lx := words(x)
	ix := hx & 0x7fffffff
	if ix >= 0x3ff00000 { // |x| >= 1
		if (ix-0x3ff00000)|int32(lx) == 0 { // |x| == 1
			if hx > 0 {
				return 0 // acos(1) = 0
			}
			return acosPi + float64(2.0*asinPio2Lo) // acos(-1) = pi
		}
		return math.NaN() // acos(|x|>1) is NaN
	}
	if ix < 0x3fe00000 { // |x| < 0.5
		if ix <= 0x3c600000 { // |x| < 2**-57
			return asinPio2Hi + asinPio2Lo
		}
		z := float64(x * x)
		p, q := asinR(z)
		r := p / q
		return asinPio2Hi - (x - (asinPio2Lo - float64(x*r)))
	} else if hx < 0 { // x < -0.5
		z := float64((1 + x) * 0.5)
		p, q := asinR(z)
		s := math.Sqrt(z)
		r := p / q
		w := float64(r*s) - asinPio2Lo
		return acosPi - float64(2.0*(s+w))
	}
	// x > 0.5
	z := float64((1 - x) * 0.5)
	s := math.Sqrt(z)
	df := math.Float64frombits(math.Float64bits(s) &^ 0xffffffff)
	c := (z - float64(df*df)) / (s + df)
	p, q := asinR(z)
	r := p / q
	w := float64(r*s) + c
	return float64(2.0 * (df + w))
}
