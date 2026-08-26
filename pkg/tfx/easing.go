package tfx

import "math"

// Easing names one of the standard easing curves. Ported from ttfx
// src/utils/easing.rs, which ports TerminalTextEffects utils/easing.py.
type Easing int

// The easing curves. Linear is the zero value, so a Path with no easing set
// moves at a constant rate.
const (
	Linear Easing = iota
	InSine
	OutSine
	InOutSine
	InQuad
	OutQuad
	InOutQuad
	InCubic
	OutCubic
	InOutCubic
	InQuart
	OutQuart
	InOutQuart
	InQuint
	OutQuint
	InOutQuint
	InExpo
	OutExpo
	InOutExpo
	InCirc
	OutCirc
	InOutCirc
	InBack
	OutBack
	InOutBack
	InElastic
	OutElastic
	InOutElastic
	InBounce
	OutBounce
	InOutBounce
)

var easingNames = map[string]Easing{
	"linear":         Linear,
	"in_sine":        InSine,
	"out_sine":       OutSine,
	"in_out_sine":    InOutSine,
	"in_quad":        InQuad,
	"out_quad":       OutQuad,
	"in_out_quad":    InOutQuad,
	"in_cubic":       InCubic,
	"out_cubic":      OutCubic,
	"in_out_cubic":   InOutCubic,
	"in_quart":       InQuart,
	"out_quart":      OutQuart,
	"in_out_quart":   InOutQuart,
	"in_quint":       InQuint,
	"out_quint":      OutQuint,
	"in_out_quint":   InOutQuint,
	"in_expo":        InExpo,
	"out_expo":       OutExpo,
	"in_out_expo":    InOutExpo,
	"in_circ":        InCirc,
	"out_circ":       OutCirc,
	"in_out_circ":    InOutCirc,
	"in_back":        InBack,
	"out_back":       OutBack,
	"in_out_back":    InOutBack,
	"in_elastic":     InElastic,
	"out_elastic":    OutElastic,
	"in_out_elastic": InOutElastic,
	"in_bounce":      InBounce,
	"out_bounce":     OutBounce,
	"in_out_bounce":  InOutBounce,
}

// ParseEasing looks up an easing curve by its upstream name.
func ParseEasing(name string) (Easing, bool) {
	e, ok := easingNames[name]
	return e, ok
}

func outBounce(p float64) float64 {
	const n1 = 7.5625
	const d1 = 2.75
	switch {
	case p < 1/d1:
		return n1 * p * p
	case p < 2/d1:
		p -= 1.5 / d1
		return n1*p*p + 0.75
	case p < 2.5/d1:
		p -= 2.25 / d1
		return n1*p*p + 0.9375
	default:
		p -= 2.625 / d1
		return n1*p*p + 0.984375
	}
}

// Ease maps a progress ratio in [0,1] through the curve.
func (e Easing) Ease(p float64) float64 {
	switch e {
	case Linear:
		return p
	case InSine:
		return 1 - math.Cos((p*math.Pi)/2)
	case OutSine:
		return math.Sin((p * math.Pi) / 2)
	case InOutSine:
		return -(math.Cos(math.Pi*p) - 1) / 2
	case InQuad:
		return math.Pow(p, 2)
	case OutQuad:
		return 1 - (1-p)*(1-p)
	case InOutQuad:
		if p < 0.5 {
			return 2 * math.Pow(p, 2)
		}
		return 1 - math.Pow(-2*p+2, 2)/2
	case InCubic:
		return math.Pow(p, 3)
	case OutCubic:
		return 1 - math.Pow(1-p, 3)
	case InOutCubic:
		if p < 0.5 {
			return 4 * math.Pow(p, 3)
		}
		return 1 - math.Pow(-2*p+2, 3)/2
	case InQuart:
		return math.Pow(p, 4)
	case OutQuart:
		return 1 - math.Pow(1-p, 4)
	case InOutQuart:
		if p < 0.5 {
			return 8 * math.Pow(p, 4)
		}
		return 1 - math.Pow(-2*p+2, 4)/2
	case InQuint:
		return math.Pow(p, 5)
	case OutQuint:
		return 1 - math.Pow(1-p, 5)
	case InOutQuint:
		if p < 0.5 {
			return 16 * math.Pow(p, 5)
		}
		return 1 - math.Pow(-2*p+2, 5)/2
	case InExpo:
		if p == 0 {
			return 0
		}
		return math.Pow(2, 10*p-10)
	case OutExpo:
		if p == 1 {
			return 1
		}
		return 1 - math.Pow(2, -10*p)
	case InOutExpo:
		switch {
		case p == 0:
			return 0
		case p == 1:
			return 1
		case p < 0.5:
			return math.Pow(2, 20*p-10) / 2
		default:
			return (2 - math.Pow(2, -20*p+10)) / 2
		}
	case InCirc:
		return 1 - math.Sqrt(1-math.Pow(p, 2))
	case OutCirc:
		return math.Sqrt(1 - math.Pow(p-1, 2))
	case InOutCirc:
		if p < 0.5 {
			return (1 - math.Sqrt(1-math.Pow(2*p, 2))) / 2
		}
		return (math.Sqrt(1-math.Pow(-2*p+2, 2)) + 1) / 2
	case InBack:
		const c1 = 1.70158
		const c3 = c1 + 1
		return c3*math.Pow(p, 3) - c1*math.Pow(p, 2)
	case OutBack:
		const c1 = 1.70158
		const c3 = c1 + 1
		return 1 + c3*math.Pow(p-1, 3) + c1*math.Pow(p-1, 2)
	case InOutBack:
		const c1 = 1.70158
		const c2 = c1 * 1.525
		if p < 0.5 {
			return (math.Pow(2*p, 2) * ((c2+1)*2*p - c2)) / 2
		}
		return (math.Pow(2*p-2, 2)*((c2+1)*(p*2-2)+c2) + 2) / 2
	case InElastic:
		const c4 = (2 * math.Pi) / 3
		switch {
		case p == 0:
			return 0
		case p == 1:
			return 1
		default:
			return -math.Pow(2, 10*p-10) * math.Sin((p*10-10.75)*c4)
		}
	case OutElastic:
		const c4 = (2 * math.Pi) / 3
		switch {
		case p == 0:
			return 0
		case p == 1:
			return 1
		default:
			return math.Pow(2, -10*p)*math.Sin((p*10-0.75)*c4) + 1
		}
	case InOutElastic:
		const c5 = (2 * math.Pi) / 4.5
		switch {
		case p == 0:
			return 0
		case p == 1:
			return 1
		case p < 0.5:
			return -(math.Pow(2, 20*p-10) * math.Sin((20*p-11.125)*c5)) / 2
		default:
			return (math.Pow(2, -20*p+10)*math.Sin((20*p-11.125)*c5))/2 + 1
		}
	case InBounce:
		return 1 - outBounce(1-p)
	case OutBounce:
		return outBounce(p)
	case InOutBounce:
		if p < 0.5 {
			return (1 - outBounce(1-2*p)) / 2
		}
		return (1 + outBounce(2*p-1)) / 2
	default:
		return p
	}
}
