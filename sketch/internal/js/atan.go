package js

import "math"

const (
	asinPio2Hi = 1.57079632679489655800e+00
	asinPio2Lo = 6.12323399573676603587e-17
	asinPio4Hi = 7.85398163397448278999e-01
	asinPS0    = 1.66666666666666657415e-01
	asinPS1    = -3.25565818622400915405e-01
	asinPS2    = 2.01212532134862925881e-01
	asinPS3    = -4.00555345006794114027e-02
	asinPS4    = 7.91534994289814532176e-04
	asinPS5    = 3.47933107596021167570e-05
	asinQS1    = -2.40339491173441421878e+00
	asinQS2    = 2.02094576023350569471e+00
	asinQS3    = -6.88283971605453293030e-01
	asinQS4    = 7.70381505559019352791e-02
)

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
	if ix >= 0x3ff00000 {
		if (ix-0x3ff00000)|int32(lx) == 0 {
			return float64(x*asinPio2Hi) + float64(x*asinPio2Lo)
		}
		return math.NaN()
	} else if ix < 0x3fe00000 {
		if ix < 0x3e400000 {
			return x
		}
		t = float64(x * x)
		p, q := asinR(t)
		w := p / q
		return x + float64(x*w)
	}
	w := 1 - math.Abs(x)
	t = float64(w * 0.5)
	p, q := asinR(t)
	s := math.Sqrt(t)
	if ix >= 0x3fef3333 {
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
	4.63647609000806093515e-01,
	7.85398163397448278999e-01,
	9.82793723247329054082e-01,
	1.57079632679489655800e+00,
}

var atanLo = [4]float64{
	2.26987774529616870924e-17,
	3.06161699786838301793e-17,
	1.39033110312309984516e-17,
	6.12323399573676603587e-17,
}

var atanT = [11]float64{
	3.33333333333329318027e-01,
	-1.99999999998764832476e-01,
	1.42857142725034663711e-01,
	-1.11111104054623557880e-01,
	9.09088713343650656196e-02,
	-7.69187620504482999495e-02,
	6.66107313738753120669e-02,
	-5.83357013379057348645e-02,
	4.97687799461593236017e-02,
	-3.65315727442169155270e-02,
	1.62858201153657823623e-02,
}

// Atan is Math.atan.
func Atan(x float64) float64 {
	hx, lx := words(x)
	ix := hx & 0x7fffffff
	if ix >= 0x44100000 {
		if ix > 0x7ff00000 || (ix == 0x7ff00000 && lx != 0) {
			return x + x
		}
		if hx > 0 {
			return atanHi[3] + atanLo[3]
		}
		return -atanHi[3] - atanLo[3]
	}
	id := -1
	if ix < 0x3fdc0000 {
		if ix < 0x3e400000 {
			return x
		}
	} else {
		x = math.Abs(x)
		if ix < 0x3ff30000 {
			if ix < 0x3fe60000 {
				id = 0
				x = (float64(2.0*x) - 1) / (2.0 + x)
			} else {
				id = 1
				x = (x - 1) / (x + 1)
			}
		} else {
			if ix < 0x40038000 {
				id = 2
				x = (x - 1.5) / (1 + float64(1.5*x))
			} else {
				id = 3
				x = -1.0 / x
			}
		}
	}
	z := float64(x * x)
	w := float64(z * z)
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
	atan2PiO4 = 7.8539816339744827900e-01
	atan2PiO2 = 1.5707963267948965580e+00
	atan2Pi   = 3.1415926535897931160e+00
	atan2PiLo = 1.2246467991473531772e-16
)

// Atan2 is Math.atan2.
func Atan2(y, x float64) float64 {
	hx, lx := words(x)
	ix := hx & 0x7fffffff
	hy, ly := words(y)
	iy := hy & 0x7fffffff
	if (uint32(ix)|((lx|-lx)>>31)) > 0x7ff00000 || (uint32(iy)|((ly|-ly)>>31)) > 0x7ff00000 {
		return x + y
	}
	if (hx-0x3ff00000)|int32(lx) == 0 {
		return Atan(y)
	}
	m := ((hy >> 31) & 1) | ((hx >> 30) & 2)

	if iy|int32(ly) == 0 {
		switch m {
		case 0, 1:
			return y
		case 2:
			return atan2Pi
		case 3:
			return -atan2Pi
		}
	}
	if ix|int32(lx) == 0 {
		if hy < 0 {
			return -atan2PiO2
		}
		return atan2PiO2
	}
	if ix == 0x7ff00000 {
		if iy == 0x7ff00000 {
			switch m {
			case 0:
				return atan2PiO4
			case 1:
				return -atan2PiO4
			case 2:
				return 3.0 * atan2PiO4
			default:
				return -3.0 * atan2PiO4
			}
		}
		switch m {
		case 0:
			return 0
		case 1:
			return math.Copysign(0, -1)
		case 2:
			return atan2Pi
		default:
			return -atan2Pi
		}
	}
	if iy == 0x7ff00000 {
		if hy < 0 {
			return -atan2PiO2
		}
		return atan2PiO2
	}

	var z float64
	k := (iy - ix) >> 20
	if k > 60 {
		z = atan2PiO2 + 0.5*atan2PiLo
		m &= 1
	} else if hx < 0 && k < -60 {
		z = 0.0
	} else {
		z = Atan(math.Abs(y / x))
	}
	switch m {
	case 0:
		return z
	case 1:
		return -z
	case 2:
		return atan2Pi - (z - atan2PiLo)
	default:
		return (z - atan2PiLo) - atan2Pi
	}
}
