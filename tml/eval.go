package tml

import (
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// ErrBudget is returned when an evaluation runs out of steps. A renderer
// shows the binding as "!".
var ErrBudget = errors.New("tml: expression step budget exhausted")

// maxString bounds a string an expression builds.
const maxString = 64 << 10

// Budget is the step allowance of one surface for one frame. Every node an
// evaluation visits costs a step, and a function costs a step per element it
// reads. Share one Budget across every binding of a frame.
type Budget struct {
	Steps int
}

// NewBudget returns a Budget of ExprSteps.
func NewBudget() *Budget { return &Budget{Steps: ExprSteps} }

func (b *Budget) spend(n int) error {
	if b == nil {
		return nil
	}
	b.Steps -= n
	if b.Steps < 0 {
		return ErrBudget
	}
	return nil
}

// Env is what an expression can read. Vars maps each root name (state, item,
// form, event, view, value, row) to its value. Values are JSON values as
// encoding/json decodes them into any: nil, bool, float64, string, []any and
// map[string]any.
type Env struct {
	Vars   map[string]any
	Now    func() time.Time // for ago(); time.Now when nil
	Budget *Budget          // nil means no limit
}

// Eval evaluates an expression. Evaluation is total: a type mismatch, a
// missing path or a division by zero gives null. The only error is
// ErrBudget.
func (e *Expr) Eval(env *Env) (any, error) {
	if env == nil {
		env = &Env{}
	}
	return eval(e.root, env)
}

func eval(n exprNode, env *Env) (any, error) {
	if err := env.Budget.spend(1); err != nil {
		return nil, err
	}
	switch x := n.(type) {
	case *litNode:
		return x.v, nil
	case *pathNode:
		return normalize(env.Vars[x.name]), nil
	case *memberNode:
		v, err := eval(x.x, env)
		if err != nil {
			return nil, err
		}
		if m, ok := v.(map[string]any); ok {
			return normalize(m[x.name]), nil
		}
		return nil, nil
	case *indexNode:
		v, err := eval(x.x, env)
		if err != nil {
			return nil, err
		}
		idx, err := eval(x.idx, env)
		if err != nil {
			return nil, err
		}
		return index(v, idx), nil
	case *unaryNode:
		v, err := eval(x.x, env)
		if err != nil {
			return nil, err
		}
		if x.op == "!" {
			return !Truthy(v), nil
		}
		if f, ok := toNum(v); ok {
			return -f, nil
		}
		return nil, nil
	case *binaryNode:
		return evalBinary(x, env)
	case *condNode:
		c, err := eval(x.c, env)
		if err != nil {
			return nil, err
		}
		if Truthy(c) {
			return eval(x.a, env)
		}
		return eval(x.b, env)
	case *arrayNode:
		out := make([]any, 0, len(x.elems))
		for _, el := range x.elems {
			v, err := eval(el, env)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
		return out, nil
	case *objectNode:
		out := make(map[string]any, len(x.keys))
		for i, k := range x.keys {
			v, err := eval(x.vals[i], env)
			if err != nil {
				return nil, err
			}
			out[k] = v
		}
		return out, nil
	case *callNode:
		args := make([]any, len(x.args))
		for i, a := range x.args {
			v, err := eval(a, env)
			if err != nil {
				return nil, err
			}
			args[i] = v
		}
		f, ok := funcs[x.name]
		if !ok {
			return nil, nil
		}
		return f(args, env)
	}
	return nil, nil
}

func index(v, idx any) any {
	switch c := v.(type) {
	case []any:
		f, ok := toNum(idx)
		if !ok || f != math.Trunc(f) || f < 0 || f >= float64(len(c)) {
			return nil
		}
		return normalize(c[int(f)])
	case map[string]any:
		if s, ok := idx.(string); ok {
			return normalize(c[s])
		}
	}
	return nil
}

func evalBinary(x *binaryNode, env *Env) (any, error) {
	l, err := eval(x.l, env)
	if err != nil {
		return nil, err
	}
	switch x.op {
	case "&&":
		if !Truthy(l) {
			return l, nil
		}
		return eval(x.r, env)
	case "||":
		if Truthy(l) {
			return l, nil
		}
		return eval(x.r, env)
	case "??":
		if l != nil {
			return l, nil
		}
		return eval(x.r, env)
	}
	r, err := eval(x.r, env)
	if err != nil {
		return nil, err
	}
	switch x.op {
	case "==":
		return equal(l, r), nil
	case "!=":
		return !equal(l, r), nil
	case "+":
		ls, lstr := l.(string)
		rs, rstr := r.(string)
		if lstr || rstr {
			if !lstr {
				ls = Stringify(l)
			}
			if !rstr {
				rs = Stringify(r)
			}
			if len(ls)+len(rs) > maxString {
				return nil, nil
			}
			if err := env.Budget.spend((len(ls) + len(rs)) / 256); err != nil {
				return nil, err
			}
			return ls + rs, nil
		}
		if la, ok := l.([]any); ok {
			if ra, ok := r.([]any); ok {
				if err := env.Budget.spend(len(la) + len(ra)); err != nil {
					return nil, err
				}
				return append(append(make([]any, 0, len(la)+len(ra)), la...), ra...), nil
			}
		}
	case "<", "<=", ">", ">=":
		c, ok := compare(l, r)
		if !ok {
			return false, nil
		}
		switch x.op {
		case "<":
			return c < 0, nil
		case "<=":
			return c <= 0, nil
		case ">":
			return c > 0, nil
		default:
			return c >= 0, nil
		}
	}
	a, aok := toNum(l)
	b, bok := toNum(r)
	if !aok || !bok {
		return nil, nil
	}
	var v float64
	switch x.op {
	case "+":
		v = a + b
	case "-":
		v = a - b
	case "*":
		v = a * b
	case "/":
		if b == 0 {
			return nil, nil
		}
		v = a / b
	case "%":
		if b == 0 {
			return nil, nil
		}
		v = math.Mod(a, b)
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil, nil
	}
	return v, nil
}

// Truthy reports whether a value counts as true: not null, false, 0, "", an
// empty array or an empty object.
func Truthy(v any) bool {
	switch x := normalize(v).(type) {
	case nil:
		return false
	case bool:
		return x
	case float64:
		return x != 0
	case string:
		return x != ""
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return true
}

// normalize turns the other numeric types into float64, so code that builds
// an Env by hand can pass int.
func normalize(v any) any {
	switch x := v.(type) {
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case float32:
		return float64(x)
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			return nil
		}
		return f
	}
	return v
}

func toNum(v any) (float64, bool) {
	switch x := normalize(v).(type) {
	case float64:
		return x, true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
	}
	return 0, false
}

func equal(a, b any) bool {
	a, b = normalize(a), normalize(b)
	switch x := a.(type) {
	case nil:
		return b == nil
	case bool, float64, string:
		return a == b
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for i := range x {
			if !equal(x[i], y[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false
		}
		for k, v := range x {
			w, ok := y[k]
			if !ok || !equal(v, w) {
				return false
			}
		}
		return true
	}
	return false
}

func compare(a, b any) (int, bool) {
	a, b = normalize(a), normalize(b)
	if x, ok := a.(float64); ok {
		if y, ok := b.(float64); ok {
			switch {
			case x < y:
				return -1, true
			case x > y:
				return 1, true
			}
			return 0, true
		}
	}
	if x, ok := a.(string); ok {
		if y, ok := b.(string); ok {
			return strings.Compare(x, y), true
		}
	}
	return 0, false
}

// Stringify formats a value as text: null is empty, a whole number has no
// decimals, and an array or object is JSON.
func Stringify(v any) string {
	switch x := normalize(v).(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return formatNum(x)
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

func formatNum(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// Funcs lists the functions an expression may call, sorted.
func Funcs() []string {
	out := make([]string, 0, len(funcs))
	for k := range funcs {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

type fn func(args []any, env *Env) (any, error)

var funcs map[string]fn

func init() {
	funcs = map[string]fn{
		"len": func(a []any, _ *Env) (any, error) {
			switch x := arg(a, 0).(type) {
			case string:
				return float64(utf8.RuneCountInString(x)), nil
			case []any:
				return float64(len(x)), nil
			case map[string]any:
				return float64(len(x)), nil
			}
			return 0.0, nil
		},
		"sum": func(a []any, env *Env) (any, error) {
			nums, err := numbers(a, env)
			var s float64
			for _, n := range nums {
				s += n
			}
			return s, err
		},
		"avg": func(a []any, env *Env) (any, error) {
			nums, err := numbers(a, env)
			if err != nil || len(nums) == 0 {
				return nil, err
			}
			var s float64
			for _, n := range nums {
				s += n
			}
			return s / float64(len(nums)), nil
		},
		"min": func(a []any, env *Env) (any, error) { return extreme(a, env, -1) },
		"max": func(a []any, env *Env) (any, error) { return extreme(a, env, 1) },
		"round": func(a []any, _ *Env) (any, error) {
			f, ok := toNum(arg(a, 0))
			if !ok {
				return nil, nil
			}
			d, _ := toNum(arg(a, 1))
			d = math.Max(0, math.Min(d, 10))
			p := math.Pow(10, math.Trunc(d))
			return math.Round(f*p) / p, nil
		},
		"abs": func(a []any, _ *Env) (any, error) {
			f, ok := toNum(arg(a, 0))
			if !ok {
				return nil, nil
			}
			return math.Abs(f), nil
		},
		"pct": func(a []any, _ *Env) (any, error) {
			f, ok := toNum(arg(a, 0))
			if !ok {
				return nil, nil
			}
			if len(a) < 2 {
				return f * 100, nil
			}
			t, ok := toNum(a[1])
			if !ok || t == 0 {
				return 0.0, nil
			}
			return f / t * 100, nil
		},
		"fmt":    fnFmt,
		"format": fnFmt,
		"dur": func(a []any, _ *Env) (any, error) {
			f, ok := toNum(arg(a, 0))
			if !ok {
				return nil, nil
			}
			unit, _ := arg(a, 1).(string)
			return formatDur(f, unit), nil
		},
		"bytes": func(a []any, _ *Env) (any, error) {
			f, ok := toNum(arg(a, 0))
			if !ok {
				return nil, nil
			}
			return formatBytes(f), nil
		},
		"ago": func(a []any, env *Env) (any, error) {
			t, ok := toTime(arg(a, 0))
			if !ok {
				return nil, nil
			}
			now := time.Now()
			if env.Now != nil {
				now = env.Now()
			}
			return formatAgo(now.Sub(t)), nil
		},
		"upper": strFn(strings.ToUpper),
		"lower": strFn(strings.ToLower),
		"trim":  strFn(strings.TrimSpace),
		"join": func(a []any, env *Env) (any, error) {
			arr, _ := arg(a, 0).([]any)
			sep := ", "
			if s, ok := arg(a, 1).(string); ok {
				sep = s
			}
			if err := env.Budget.spend(len(arr)); err != nil {
				return nil, err
			}
			parts := make([]string, len(arr))
			n := 0
			for i, v := range arr {
				parts[i] = Stringify(v)
				n += len(parts[i]) + len(sep)
				if n > maxString {
					return nil, nil
				}
			}
			return strings.Join(parts, sep), nil
		},
		"contains": func(a []any, env *Env) (any, error) {
			switch x := arg(a, 0).(type) {
			case string:
				s, ok := arg(a, 1).(string)
				return ok && strings.Contains(x, s), nil
			case []any:
				if err := env.Budget.spend(len(x)); err != nil {
					return nil, err
				}
				for _, v := range x {
					if equal(v, arg(a, 1)) {
						return true, nil
					}
				}
			}
			return false, nil
		},
		"starts": func(a []any, _ *Env) (any, error) {
			s, ok1 := arg(a, 0).(string)
			p, ok2 := arg(a, 1).(string)
			return ok1 && ok2 && strings.HasPrefix(s, p), nil
		},
		"pick": func(a []any, env *Env) (any, error) {
			arr, _ := arg(a, 0).([]any)
			f, _ := arg(a, 1).(string)
			if err := env.Budget.spend(len(arr)); err != nil {
				return nil, err
			}
			out := make([]any, len(arr))
			for i, v := range arr {
				if m, ok := v.(map[string]any); ok {
					out[i] = normalize(m[f])
				}
			}
			return out, nil
		},
		"count": func(a []any, env *Env) (any, error) {
			arr, _ := arg(a, 0).([]any)
			if err := env.Budget.spend(len(arr)); err != nil {
				return nil, err
			}
			if len(a) < 2 {
				return float64(len(arr)), nil
			}
			f, _ := a[1].(string)
			n := 0
			for _, v := range arr {
				m, ok := v.(map[string]any)
				if !ok {
					continue
				}
				if len(a) < 3 && Truthy(m[f]) || len(a) >= 3 && equal(m[f], a[2]) {
					n++
				}
			}
			return float64(n), nil
		},
	}
}

func arg(a []any, i int) any {
	if i < len(a) {
		return normalize(a[i])
	}
	return nil
}

func strFn(f func(string) string) fn {
	return func(a []any, _ *Env) (any, error) {
		s, ok := arg(a, 0).(string)
		if !ok {
			return nil, nil
		}
		return f(s), nil
	}
}

// numbers reads the numbers of a call: one array, optionally with a field
// name for an array of rows, or several numbers.
func numbers(a []any, env *Env) ([]float64, error) {
	var out []float64
	if arr, ok := arg(a, 0).([]any); ok {
		if err := env.Budget.spend(len(arr)); err != nil {
			return nil, err
		}
		field, _ := arg(a, 1).(string)
		for _, v := range arr {
			if field != "" {
				m, _ := v.(map[string]any)
				v = m[field]
			}
			if f, ok := toNum(v); ok {
				out = append(out, f)
			}
		}
		return out, nil
	}
	for _, v := range a {
		if f, ok := toNum(v); ok {
			out = append(out, f)
		}
	}
	return out, nil
}

func extreme(a []any, env *Env, sign float64) (any, error) {
	nums, err := numbers(a, env)
	if err != nil || len(nums) == 0 {
		return nil, err
	}
	best := nums[0]
	for _, n := range nums[1:] {
		if (n-best)*sign > 0 {
			best = n
		}
	}
	return best, nil
}

// fnFmt formats a number with thousands separators and a fixed number of
// decimals: fmt(1234.5) is 1,234.5 and fmt(1234.5, 0) is 1,235. A string is
// returned as it is.
func fnFmt(a []any, _ *Env) (any, error) {
	v := arg(a, 0)
	f, ok := v.(float64)
	if !ok {
		return Stringify(v), nil
	}
	digits := -1
	if d, ok := toNum(arg(a, 1)); ok {
		digits = int(math.Max(0, math.Min(d, 10)))
		p := math.Pow(10, float64(digits))
		f = math.Round(f*p) / p // half away from zero, as people round
	}
	s := strconv.FormatFloat(math.Abs(f), 'f', digits, 64)
	intPart, frac, _ := strings.Cut(s, ".")
	var b strings.Builder
	if f < 0 && s != strings.Repeat("0", len(s)) {
		b.WriteByte('-')
	}
	for i, c := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if frac != "" {
		b.WriteByte('.')
		b.WriteString(frac)
	}
	return b.String(), nil
}

var durUnits = map[string]float64{"ns": 1e-9, "us": 1e-6, "µs": 1e-6, "ms": 1e-3, "s": 1, "m": 60, "h": 3600}

// formatDur formats an amount of unit (default ms) as a short duration:
// 120µs, 840ms, 2.21s, 4m 5s, 1h 4m.
func formatDur(v float64, unit string) string {
	scale, ok := durUnits[unit]
	if !ok {
		scale = 1e-3
	}
	s := v * scale
	sign := ""
	if s < 0 {
		sign, s = "-", -s
	}
	switch {
	case s == 0:
		return "0s"
	case s < 1e-3:
		return sign + trimZeros(strconv.FormatFloat(s*1e6, 'f', 0, 64)) + "µs"
	case s < 1:
		return sign + strconv.FormatFloat(math.Round(s*1e3), 'f', 0, 64) + "ms"
	case s < 60:
		return sign + trimZeros(strconv.FormatFloat(s, 'f', 2, 64)) + "s"
	case s < 3600:
		m := math.Floor(s / 60)
		return sign + formatNum(m) + "m " + formatNum(math.Floor(s-m*60)) + "s"
	default:
		h := math.Floor(s / 3600)
		return sign + formatNum(h) + "h " + formatNum(math.Floor((s-h*3600)/60)) + "m"
	}
}

func trimZeros(s string) string {
	if !strings.Contains(s, ".") {
		return s
	}
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

// formatBytes formats a byte count with binary units: 512B, 1.5K, 3.1G.
func formatBytes(v float64) string {
	units := []string{"B", "K", "M", "G", "T", "P"}
	sign := ""
	if v < 0 {
		sign, v = "-", -v
	}
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if i == 0 || v >= 10 {
		return sign + strconv.FormatFloat(math.Round(v), 'f', 0, 64) + units[i]
	}
	return sign + trimZeros(strconv.FormatFloat(v, 'f', 1, 64)) + units[i]
}

func toTime(v any) (time.Time, bool) {
	switch x := v.(type) {
	case float64:
		if math.IsNaN(x) || math.Abs(x) > 1e12 {
			return time.Time{}, false
		}
		sec, frac := math.Modf(x)
		return time.Unix(int64(sec), int64(frac*1e9)), true
	case string:
		t, err := time.Parse(time.RFC3339, x)
		return t, err == nil
	}
	return time.Time{}, false
}

// formatAgo formats how long ago a moment was: just now, 5s ago, 3m ago,
// 2h ago, 4d ago, or in 3m for a moment ahead.
func formatAgo(d time.Duration) string {
	future := d < 0
	if future {
		d = -d
	}
	var s string
	switch {
	case d < 5*time.Second:
		return "just now"
	case d < time.Minute:
		s = strconv.Itoa(int(d.Seconds())) + "s"
	case d < time.Hour:
		s = strconv.Itoa(int(d.Minutes())) + "m"
	case d < 48*time.Hour:
		s = strconv.Itoa(int(d.Hours())) + "h"
	default:
		s = strconv.Itoa(int(d.Hours()/24)) + "d"
	}
	if future {
		return "in " + s
	}
	return s + " ago"
}
