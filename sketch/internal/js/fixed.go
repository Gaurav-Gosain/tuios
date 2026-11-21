package js

import (
	"strconv"
	"strings"
)

func exact(x float64) (digits []byte, exp int) {
	var buf [800]byte
	s := strconv.AppendFloat(buf[:0], x, 'e', 767, 64)
	e := strings.LastIndexByte(string(s), 'e')
	exp, _ = strconv.Atoi(string(s[e+1:]))
	digits = make([]byte, 0, e)
	digits = append(digits, s[0])
	digits = append(digits, s[2:e]...)
	for len(digits) > 1 && digits[len(digits)-1] == '0' {
		digits = digits[:len(digits)-1]
	}
	return digits, exp
}

func roundDigits(digits []byte, n int) ([]byte, bool) {
	out := make([]byte, n)
	for i := range out {
		if i < len(digits) {
			out[i] = digits[i]
		} else {
			out[i] = '0'
		}
	}
	if n >= len(digits) || digits[n] < '5' {
		return out, false
	}
	for i := n - 1; i >= 0; i-- {
		if out[i] < '9' {
			out[i]++
			return out, false
		}
		out[i] = '0'
	}
	if n > 0 {
		out[0] = '1'
	}
	return out, true
}

// ToFixed is x.toFixed(f).
func ToFixed(x float64, f int) string {
	if x != x {
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
	var whole []byte
	if x == 0 {
		whole = []byte{'0'}
	} else {
		digits, exp := exact(x)
		n := exp + 1 + f
		if n < 0 {
			whole = []byte{'0'}
		} else {
			kept, carry := roundDigits(digits, n)
			if n == 0 {
				if carry || (len(digits) > 0 && digits[0] >= '5') {
					kept = []byte{'1'}
				} else {
					kept = []byte{'0'}
				}
			} else if carry {
				kept = append(kept, '0')
			}
			whole = kept
		}
	}
	for len(whole) < f+1 {
		whole = append([]byte{'0'}, whole...)
	}
	for len(whole) > f+1 && whole[0] == '0' {
		whole = whole[1:]
	}
	if f == 0 {
		return sign + string(whole)
	}
	cut := len(whole) - f
	return sign + string(whole[:cut]) + "." + string(whole[cut:])
}

// ToExponential is x.toExponential(f).
func ToExponential(x float64, f int) string {
	if x != x {
		return "NaN"
	}
	sign := ""
	if x < 0 {
		sign = "-"
		x = -x
	}
	if x > 1.7976931348623157e308 {
		return sign + "Infinity"
	}
	var kept []byte
	exp := 0
	if x == 0 {
		kept = []byte(strings.Repeat("0", f+1))
	} else {
		digits, e := exact(x)
		var carry bool
		kept, carry = roundDigits(digits, f+1)
		exp = e
		if carry {
			exp++
		}
	}
	out := sign + string(kept[:1])
	if f > 0 {
		out += "." + string(kept[1:])
	}
	if exp < 0 {
		return out + "e-" + strconv.Itoa(-exp)
	}
	return out + "e+" + strconv.Itoa(exp)
}

// PrecisionNumber is Number(x.toPrecision(p)): x rounded half up to p significant digits.
func PrecisionNumber(x float64, p int) float64 {
	if x != x || x == 0 || x > 1.7976931348623157e308 || x < -1.7976931348623157e308 {
		return x
	}
	sign := ""
	if x < 0 {
		sign = "-"
		x = -x
	}
	digits, exp := exact(x)
	kept, carry := roundDigits(digits, p)
	if carry {
		exp++
	}
	v, _ := strconv.ParseFloat(sign+"0."+string(kept)+"e"+strconv.Itoa(exp+1), 64)
	return v
}
