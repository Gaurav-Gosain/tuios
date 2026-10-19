package js

import "math"

// Sin and Cos are Math.sin and Math.cos as V8 computes them: fdlibm's kernels and its
// argument reduction, ported from V8's src/base/ieee754.cc. Go's math.Sin and math.Cos
// use other polynomials and can differ in the last bit. Products are converted to
// float64 so that the compiler does not fuse them into FMA instructions, which would
// round differently. Past 2^19 times pi/2 the reduction needs fdlibm's long
// __kernel_rem_pio2, and the port falls back to Go's functions there.

const (
	sinS1 = -1.66666666666666324348e-01
	sinS2 = 8.33333333332248946124e-03
	sinS3 = -1.98412698298579493134e-04
	sinS4 = 2.75573137070700676789e-06
	sinS5 = -2.50507602534068634195e-08
	sinS6 = 1.58969099521155010221e-10

	cosC1 = 4.16666666666666019037e-02
	cosC2 = -1.38888888888741095749e-03
	cosC3 = 2.48015872894767294178e-05
	cosC4 = -2.75573143513906633035e-07
	cosC5 = 2.08757232129817482790e-09
	cosC6 = -1.13596475577881948265e-11

	invpio2 = 6.36619772367581382433e-01
	pio2_1  = 1.57079632673412561417e+00
	pio2_1t = 6.07710050650619224932e-11
	pio2_2  = 6.07710050630396597660e-11
	pio2_2t = 2.02226624879595063154e-21
	pio2_3  = 2.02226624871116645580e-21
	pio2_3t = 8.47842766036889956997e-32
)

// The high words of n*pi/2 for n from 1 to 32.
var npio2_hw = [32]uint32{
	0x3FF921FB, 0x400921FB, 0x4012D97C, 0x401921FB, 0x401F6A7A, 0x4022D97C,
	0x4025FDBB, 0x402921FB, 0x402C463A, 0x402F6A7A, 0x4031475C, 0x4032D97C,
	0x40346B9C, 0x4035FDBB, 0x40378FDB, 0x403921FB, 0x403AB41B, 0x403C463A,
	0x403DD85A, 0x403F6A7A, 0x40407E4C, 0x4041475C, 0x4042106C, 0x4042D97C,
	0x4043A28C, 0x40446B9C, 0x404534AC, 0x4045FDBB, 0x4046C6CB, 0x40478FDB,
	0x404858EB, 0x404921FB,
}

func highWord(x float64) uint32 { return uint32(math.Float64bits(x) >> 32) }

func kernelSin(x, y float64, iy int) float64 {
	ix := highWord(x) & 0x7fffffff
	if ix < 0x3e400000 && int64(x) == 0 {
		return x
	}
	z := float64(x * x)
	v := float64(z * x)
	r := sinS2 + float64(z*(sinS3+float64(z*(sinS4+float64(z*(sinS5+float64(z*sinS6)))))))
	if iy == 0 {
		return x + float64(v*(sinS1+float64(z*r)))
	}
	return x - ((float64(z*(float64(0.5*y)-float64(v*r))) - y) - float64(v*sinS1))
}

func kernelCos(x, y float64) float64 {
	ix := highWord(x) & 0x7fffffff
	if ix < 0x3e400000 && int64(x) == 0 {
		return 1
	}
	z := float64(x * x)
	r := float64(z * (cosC1 + float64(z*(cosC2+float64(z*(cosC3+float64(z*(cosC4+float64(z*(cosC5+float64(z*cosC6)))))))))))
	if ix < 0x3FD33333 {
		return 1 - (float64(0.5*z) - (float64(z*r) - float64(x*y)))
	}
	var qx float64
	if ix > 0x3fe90000 {
		qx = 0.28125
	} else {
		qx = math.Float64frombits(uint64(ix-0x00200000) << 32)
	}
	hz := float64(0.5*z) - qx
	a := 1 - qx
	return a - (hz - (float64(z*r) - float64(x*y)))
}

// remPio2 reduces x to y0 + y1 in [-pi/4, pi/4] and returns n, with x = n*pi/2 + y0 + y1.
// ok is false past the medium range.
func remPio2(x float64) (n int32, y0, y1 float64, ok bool) {
	hx := int32(highWord(x))
	ix := uint32(hx) & 0x7fffffff
	if ix <= 0x3fe921fb {
		return 0, x, 0, true
	}
	if ix < 0x4002d97c {
		if hx > 0 {
			z := x - pio2_1
			if ix != 0x3ff921fb {
				y0 = z - pio2_1t
				y1 = (z - y0) - pio2_1t
			} else {
				z -= pio2_2
				y0 = z - pio2_2t
				y1 = (z - y0) - pio2_2t
			}
			return 1, y0, y1, true
		}
		z := x + pio2_1
		if ix != 0x3ff921fb {
			y0 = z + pio2_1t
			y1 = (z - y0) + pio2_1t
		} else {
			z += pio2_2
			y0 = z + pio2_2t
			y1 = (z - y0) + pio2_2t
		}
		return -1, y0, y1, true
	}
	if ix > 0x413921fb {
		return 0, 0, 0, false
	}
	t := math.Abs(x)
	n = int32(float64(t*invpio2) + 0.5)
	fn := float64(n)
	r := t - float64(fn*pio2_1)
	w := float64(fn * pio2_1t)
	if n < 32 && ix != npio2_hw[n-1] {
		y0 = r - w
	} else {
		j := ix >> 20
		y0 = r - w
		i := int32(j) - int32((highWord(y0)>>20)&0x7ff)
		if i > 16 {
			t = r
			w = float64(fn * pio2_2)
			r = t - w
			w = float64(fn*pio2_2t) - ((t - r) - w)
			y0 = r - w
			i = int32(j) - int32((highWord(y0)>>20)&0x7ff)
			if i > 49 {
				t = r
				w = float64(fn * pio2_3)
				r = t - w
				w = float64(fn*pio2_3t) - ((t - r) - w)
				y0 = r - w
			}
		}
	}
	y1 = (r - y0) - w
	if hx < 0 {
		return -n, -y0, -y1, true
	}
	return n, y0, y1, true
}

// Sin is Math.sin.
func Sin(x float64) float64 {
	ix := highWord(x) & 0x7fffffff
	if ix <= 0x3fe921fb {
		return kernelSin(x, 0, 0)
	}
	if ix >= 0x7ff00000 {
		return math.NaN()
	}
	n, y0, y1, ok := remPio2(x)
	if !ok {
		return math.Sin(x)
	}
	switch n & 3 {
	case 0:
		return kernelSin(y0, y1, 1)
	case 1:
		return kernelCos(y0, y1)
	case 2:
		return -kernelSin(y0, y1, 1)
	}
	return -kernelCos(y0, y1)
}

// Cos is Math.cos.
func Cos(x float64) float64 {
	ix := highWord(x) & 0x7fffffff
	if ix <= 0x3fe921fb {
		return kernelCos(x, 0)
	}
	if ix >= 0x7ff00000 {
		return math.NaN()
	}
	n, y0, y1, ok := remPio2(x)
	if !ok {
		return math.Cos(x)
	}
	switch n & 3 {
	case 0:
		return kernelCos(y0, y1)
	case 1:
		return -kernelSin(y0, y1, 1)
	case 2:
		return -kernelCos(y0, y1)
	}
	return kernelSin(y0, y1, 1)
}

// ToFixed0 is Number.prototype.toFixed(0): the nearest integer, the larger one on a tie,
// with the sign of a negative number kept ("-0").
func ToFixed0(x float64) string {
	if math.IsNaN(x) {
		return "NaN"
	}
	if x >= 1e21 || x <= -1e21 {
		return Num(x)
	}
	sign := ""
	if x < 0 {
		sign = "-"
		x = -x
	}
	f := math.Floor(x)
	if x-f >= 0.5 {
		f++
	}
	return sign + Num(f)
}
