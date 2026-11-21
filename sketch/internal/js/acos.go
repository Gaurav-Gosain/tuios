package js

import "math"

const acosPi = 3.14159265358979311600e+00

// Acos is Math.acos.
func Acos(x float64) float64 {
	hx, lx := words(x)
	ix := hx & 0x7fffffff
	if ix >= 0x3ff00000 {
		if (ix-0x3ff00000)|int32(lx) == 0 {
			if hx > 0 {
				return 0
			}
			return acosPi + float64(2.0*asinPio2Lo)
		}
		return math.NaN()
	}
	if ix < 0x3fe00000 {
		if ix <= 0x3c600000 {
			return asinPio2Hi + asinPio2Lo
		}
		z := float64(x * x)
		p, q := asinR(z)
		r := p / q
		return asinPio2Hi - (x - (asinPio2Lo - float64(x*r)))
	} else if hx < 0 {
		z := float64((1 + x) * 0.5)
		p, q := asinR(z)
		s := math.Sqrt(z)
		r := p / q
		w := float64(r*s) - asinPio2Lo
		return acosPi - float64(2.0*(s+w))
	}
	z := float64((1 - x) * 0.5)
	s := math.Sqrt(z)
	df := math.Float64frombits(math.Float64bits(s) &^ 0xffffffff)
	c := (z - float64(df*df)) / (s + df)
	p, q := asinR(z)
	r := p / q
	w := float64(r*s) + c
	return float64(2.0 * (df + w))
}
