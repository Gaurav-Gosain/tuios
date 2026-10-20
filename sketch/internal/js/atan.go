package js

import "math"

// Asin, Atan and Atan2 are Math.asin, Math.atan and Math.atan2 as V8 computes them:
// fdlibm's e_asin.c, s_atan.c and e_atan2.c, ported from V8's src/base/ieee754.cc in
// V8 13.6 (node 24). Newer V8 calls LLVM's libc for these instead, which rounds
// differently, so the port follows the V8 the goldens were written with. Go's math.Asin
// and math.Atan use Cephes polynomials and differ in the last bit for many inputs.
// Products are converted to float64 so that the compiler does not fuse them into FMA
// instructions.

const (
	asinPio2Hi = 1.57079632679489655800e+00 // 0x3FF921FB, 0x54442D18
	asinPio2Lo = 6.12323399573676603587e-17 // 0x3C91A626, 0x33145C07
	asinPio4Hi = 7.85398163397448278999e-01 // 0x3FE921FB, 0x54442D18
	// coefficients for R(x^2)
	asinPS0 = 1.66666666666666657415e-01  // 0x3FC55555, 0x55555555
	asinPS1 = -3.25565818622400915405e-01 // 0xBFD4D612, 0x03EB6F7D
	asinPS2 = 2.01212532134862925881e-01  // 0x3FC9C155, 0x0E884455
	asinPS3 = -4.00555345006794114027e-02 // 0xBFA48228, 0xB5688F3B
	asinPS4 = 7.91534994289814532176e-04  // 0x3F49EFE0, 0x7501B288
	asinPS5 = 3.47933107596021167570e-05  // 0x3F023DE1, 0x0DFDF709
	asinQS1 = -2.40339491173441421878e+00 // 0xC0033A27, 0x1C8A2D4B
	asinQS2 = 2.02094576023350569471e+00  // 0x40002AE5, 0x9C598AC8
	asinQS3 = -6.88283971605453293030e-01 // 0xBFE6066C, 0x1B8D0159
	asinQS4 = 7.70381505559019352791e-02  // 0x3FB3B8C5, 0xB12E9282
)

// asinR returns p and q of the rational approximation R(t) = p/q.
func asinR(t float64) (p, q float64) {
	p = float64(t * (asinPS0 + float64(t*(asinPS1+float64(t*(asinPS2+float64(t*(asinPS3+float64(t*(asinPS4+float64(t*asinPS5)))))))))))
	q = 1 + float64(t*(asinQS1+float64(t*(asinQS2+float64(t*(asinQS3+float64(t*asinQS4)))))))
	return p, q
}

// Asin is Math.asin.
func Asin(x float64) float64 {
	hx, lx := words(x)
	ix := hx & 0x7fffffff
	t := 0.0
	if ix >= 0x3ff00000 { // |x| >= 1
		if (ix-0x3ff00000)|int32(lx) == 0 { // asin(1) = +-pi/2 with inexact
			return float64(x*asinPio2Hi) + float64(x*asinPio2Lo)
		}
		return math.NaN() // asin(|x|>1) is NaN
	} else if ix < 0x3fe00000 { // |x| < 0.5
		if ix < 0x3e400000 { // |x| < 2**-27
			return x
		}
		t = float64(x * x)
		p, q := asinR(t)
		w := p / q
		return x + float64(x*w)
	}
	// 1 > |x| >= 0.5
	w := 1 - math.Abs(x)
	t = float64(w * 0.5)
	p, q := asinR(t)
	s := math.Sqrt(t)
	if ix >= 0x3fef3333 { // |x| > 0.975
		w = p / q
		t = asinPio2Hi - (float64(2.0*(s+float64(s*w))) - asinPio2Lo)
	} else {
		w = math.Float64frombits(math.Float64bits(s) &^ 0xffffffff)
		c := (t - float64(w*w)) / (s + w)
		r := p / q
		p = float64(float64(2.0*s)*r) - (asinPio2Lo - float64(2.0*c))
		q = asinPio4Hi - float64(2.0*w)
		t = asinPio4Hi - (p - q)
	}
	if hx > 0 {
		return t
	}
	return -t
}

var atanHi = [4]float64{
	4.63647609000806093515e-01, // atan(0.5)hi 0x3FDDAC67, 0x0561BB4F
	7.85398163397448278999e-01, // atan(1.0)hi 0x3FE921FB, 0x54442D18
	9.82793723247329054082e-01, // atan(1.5)hi 0x3FEF730B, 0xD281F69B
	1.57079632679489655800e+00, // atan(inf)hi 0x3FF921FB, 0x54442D18
}

var atanLo = [4]float64{
	2.26987774529616870924e-17, // atan(0.5)lo 0x3C7A2B7F, 0x222F65E2
	3.06161699786838301793e-17, // atan(1.0)lo 0x3C81A626, 0x33145C07
	1.39033110312309984516e-17, // atan(1.5)lo 0x3C700788, 0x7AF0CBBD
	6.12323399573676603587e-17, // atan(inf)lo 0x3C91A626, 0x33145C07
}

var atanT = [11]float64{
	3.33333333333329318027e-01,  // 0x3FD55555, 0x5555550D
	-1.99999999998764832476e-01, // 0xBFC99999, 0x9998EBC4
	1.42857142725034663711e-01,  // 0x3FC24924, 0x920083FF
	-1.11111104054623557880e-01, // 0xBFBC71C6, 0xFE231671
	9.09088713343650656196e-02,  // 0x3FB745CD, 0xC54C206E
	-7.69187620504482999495e-02, // 0xBFB3B0F2, 0xAF749A6D
	6.66107313738753120669e-02,  // 0x3FB10D66, 0xA0D03D51
	-5.83357013379057348645e-02, // 0xBFADDE2D, 0x52DEFD9A
	4.97687799461593236017e-02,  // 0x3FA97B4B, 0x24760DEB
	-3.65315727442169155270e-02, // 0xBFA2B444, 0x2C6A6C2F
	1.62858201153657823623e-02,  // 0x3F90AD3A, 0xE322DA11
}

// Atan is Math.atan.
func Atan(x float64) float64 {
	hx, lx := words(x)
	ix := hx & 0x7fffffff
	if ix >= 0x44100000 { // |x| >= 2^66
		if ix > 0x7ff00000 || (ix == 0x7ff00000 && lx != 0) {
			return x + x // NaN
		}
		if hx > 0 {
			return atanHi[3] + atanLo[3]
		}
		return -atanHi[3] - atanLo[3]
	}
	id := -1
	if ix < 0x3fdc0000 { // |x| < 0.4375
		if ix < 0x3e400000 { // |x| < 2^-27
			return x
		}
	} else {
		x = math.Abs(x)
		if ix < 0x3ff30000 { // |x| < 1.1875
			if ix < 0x3fe60000 { // 7/16 <= |x| < 11/16
				id = 0
				x = (float64(2.0*x) - 1) / (2.0 + x)
			} else { // 11/16 <= |x| < 19/16
				id = 1
				x = (x - 1) / (x + 1)
			}
		} else {
			if ix < 0x40038000 { // |x| < 2.4375
				id = 2
				x = (x - 1.5) / (1 + float64(1.5*x))
			} else { // 2.4375 <= |x| < 2^66
				id = 3
				x = -1.0 / x
			}
		}
	}
	// end of argument reduction
	z := float64(x * x)
	w := float64(z * z)
	// break sum from i=0 to 10 aT[i]z**(i+1) into odd and even poly
	s1 := float64(w * atanT[10])
	s1 = float64(w * (atanT[8] + s1))
	s1 = float64(w * (atanT[6] + s1))
	s1 = float64(w * (atanT[4] + s1))
	s1 = float64(w * (atanT[2] + s1))
	s1 = float64(z * (atanT[0] + s1))
	s2 := float64(w * atanT[9])
	s2 = float64(w * (atanT[7] + s2))
	s2 = float64(w * (atanT[5] + s2))
	s2 = float64(w * (atanT[3] + s2))
	s2 = float64(w * (atanT[1] + s2))
	if id < 0 {
		return x - float64(x*(s1+s2))
	}
	z = atanHi[id] - ((float64(x*(s1+s2)) - atanLo[id]) - x)
	if hx < 0 {
		return -z
	}
	return z
}

const (
	atan2PiO4 = 7.8539816339744827900e-01 // 0x3FE921FB, 0x54442D18
	atan2PiO2 = 1.5707963267948965580e+00 // 0x3FF921FB, 0x54442D18
	atan2Pi   = 3.1415926535897931160e+00 // 0x400921FB, 0x54442D18
	atan2PiLo = 1.2246467991473531772e-16 // 0x3CA1A626, 0x33145C07
)

// Atan2 is Math.atan2.
func Atan2(y, x float64) float64 {
	// fdlibm adds a tiny 1e-300 to raise inexact; it never changes a rounded result.
	hx, lx := words(x)
	ix := hx & 0x7fffffff
	hy, ly := words(y)
	iy := hy & 0x7fffffff
	if (uint32(ix)|((lx|-lx)>>31)) > 0x7ff00000 || (uint32(iy)|((ly|-ly)>>31)) > 0x7ff00000 {
		return x + y // x or y is NaN
	}
	if (hx-0x3ff00000)|int32(lx) == 0 {
		return Atan(y) // x=1.0
	}
	m := ((hy >> 31) & 1) | ((hx >> 30) & 2) // 2*sign(x)+sign(y)

	// when y = 0
	if iy|int32(ly) == 0 {
		switch m {
		case 0, 1:
			return y // atan(+-0,+anything)=+-0
		case 2:
			return atan2Pi // atan(+0,-anything) = pi
		case 3:
			return -atan2Pi // atan(-0,-anything) =-pi
		}
	}
	// when x = 0
	if ix|int32(lx) == 0 {
		if hy < 0 {
			return -atan2PiO2
		}
		return atan2PiO2
	}
	// when x is INF
	if ix == 0x7ff00000 {
		if iy == 0x7ff00000 {
			switch m {
			case 0:
				return atan2PiO4 // atan(+INF,+INF)
			case 1:
				return -atan2PiO4 // atan(-INF,+INF)
			case 2:
				return 3.0 * atan2PiO4 // atan(+INF,-INF)
			default:
				return -3.0 * atan2PiO4 // atan(-INF,-INF)
			}
		}
		switch m {
		case 0:
			return 0 // atan(+...,+INF)
		case 1:
			return math.Copysign(0, -1) // atan(-...,+INF)
		case 2:
			return atan2Pi // atan(+...,-INF)
		default:
			return -atan2Pi // atan(-...,-INF)
		}
	}
	// when y is INF
	if iy == 0x7ff00000 {
		if hy < 0 {
			return -atan2PiO2
		}
		return atan2PiO2
	}

	// compute y/x
	var z float64
	k := (iy - ix) >> 20
	if k > 60 { // |y/x| > 2**60
		z = atan2PiO2 + 0.5*atan2PiLo
		m &= 1
	} else if hx < 0 && k < -60 {
		z = 0.0 // 0 > |y|/x > -2**-60
	} else {
		z = Atan(math.Abs(y / x)) // safe to do y/x
	}
	switch m {
	case 0:
		return z // atan(+,+)
	case 1:
		return -z // atan(-,+)
	case 2:
		return atan2Pi - (z - atan2PiLo) // atan(+,-)
	default: // case 3
		return (z - atan2PiLo) - atan2Pi // atan(-,-)
	}
}
