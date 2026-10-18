package tml

import (
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// ErrBudget is returned when an evaluation runs out of steps. A renderer
// shows the binding as "!".
var ErrBudget = errors.New("tml: expression step budget exhausted")

// errTooLong stops a string an expression builds at maxString.
var errTooLong = errors.New("tml: string too long")

// maxString bounds a string an expression builds.
const maxString = 64 << 10

// Budget is the step allowance of one surface for one frame. Every node an
// evaluation visits costs a step. Work that grows with its input costs more:
// a function pays a step per element it reads, and text pays a step per 32
// bytes it reads or writes. Share one Budget across every binding of a
// frame.
type Budget struct {
	Steps int
}

// NewBudget returns a Budget of ExprSteps.
func NewBudget() *Budget { return &Budget{Steps: ExprSteps} }

func (b *Budget) spend(n int) error {
	b.Steps -= n
	if b.Steps < 0 {
		return ErrBudget
	}
	return nil
}

// spendBytes charges for n bytes of text.
func (b *Budget) spendBytes(n int) error { return b.spend(1 + n/32) }

// Env is what an expression can read. Vars maps each root name (state, item,
// form, event, view, value, row) to its value. Values are JSON values as
// encoding/json decodes them into any: nil, bool, float64, string, []any and
// map[string]any.
type Env struct {
	Vars   map[string]any
	Now    func() time.Time // for ago(); time.Now when nil
	Budget *Budget          // nil means a fresh NewBudget for each call
}

// Eval evaluates an expression. Evaluation is total: a type mismatch, a
// missing path, a division by zero or a result that is not finite gives
// null. The only error is ErrBudget. Every string and key in the result
// passes through Sanitize.
func (e *Expr) Eval(env *Env) (any, error) {
	env = withBudget(env)
	v, err := eval(e.root, env)
	if err != nil {
		return nil, err
	}
	return sanitizeCharged(v, env.Budget)
}

func withBudget(env *Env) *Env {
	if env == nil {
		return &Env{Budget: NewBudget()}
	}
	if env.Budget == nil {
		e := *env
		e.Budget = NewBudget()
		return &e
	}
	return env
}

// sanitizeCharged sanitizes a result and charges for the scan: a step per
// value and per 32 bytes of string and key. A string result longer than
// 64 KiB is cut first, so a large state string cannot be scanned in full
// for every binding.
func sanitizeCharged(v any, b *Budget) (any, error) {
	if s, ok := v.(string); ok {
		s = cutRunes(s, maxString)
		if err := b.spendBytes(len(s)); err != nil {
			return nil, err
		}
		return Sanitize(s), nil
	}
	cost := 0
	var count func(any)
	count = func(v any) {
		cost++
		switch x := v.(type) {
		case string:
			cost += len(x) / 32
		case []any:
			for _, e := range x {
				count(e)
			}
		case map[string]any:
			for k, e := range x {
				cost += len(k) / 32
				count(e)
			}
		}
	}
	count(v)
	if err := b.spend(cost); err != nil {
		return nil, err
	}
	return SanitizeValue(v), nil
}

// finite turns a number that is not finite into null.
func finite(v any) any {
	if f, ok := v.(float64); ok && (math.IsNaN(f) || math.IsInf(f, 0)) {
		return nil
	}
	return v
}

func eval(n exprNode, env *Env) (any, error) {
	if err := env.Budget.spend(1); err != nil {
		return nil, err
	}
	switch x := n.(type) {
	case *litNode:
		return x.v, nil
	case *pathNode:
		return finite(normalize(env.Vars[x.name])), nil
	case *memberNode:
		v, err := eval(x.x, env)
		if err != nil {
			return nil, err
		}
		if m, ok := v.(map[string]any); ok {
			return finite(normalize(m[x.name])), nil
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
		return finite(index(v, idx)), nil
	case *unaryNode:
		v, err := eval(x.x, env)
		if err != nil {
			return nil, err
		}
		if x.op == "!" {
			return !Truthy(v), nil
		}
		if f, ok := toNum(v); ok {
			return finite(-f), nil
		}
		return nil, nil
	case *binaryNode:
		v, err := evalBinary(x, env)
		return finite(v), err
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
		v, err := call(x.name, args, env)
		return finite(v), err
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
	case "&&", "||":
		if Truthy(l) == (x.op == "||") {
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
	case "==", "!=":
		eq, err := equalB(l, r, env.Budget)
		return eq == (x.op == "=="), err
	case "+":
		ls, lstr := l.(string)
		rs, rstr := r.(string)
		if lstr || rstr {
			var ok bool
			if !lstr {
				if ls, ok, err = str(l, env.Budget); err != nil || !ok {
					return nil, err
				}
			}
			if !rstr {
				if rs, ok, err = str(r, env.Budget); err != nil || !ok {
					return nil, err
				}
			}
			if len(ls)+len(rs) > maxString {
				return nil, nil
			}
			if err := env.Budget.spendBytes(len(ls) + len(rs)); err != nil {
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
		c, ok, err := compareB(l, r, env.Budget)
		if err != nil || !ok {
			return false, err
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
	switch x.op {
	case "+":
		return a + b, nil
	case "-":
		return a - b, nil
	case "*":
		return a * b, nil
	case "/":
		if b == 0 {
			return nil, nil
		}
		return a / b, nil
	case "%":
		if b == 0 {
			return nil, nil
		}
		return math.Mod(a, b), nil
	}
	return nil, nil
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
// an Env by hand can pass int. Any value outside the JSON model, such as a
// []string, reads as null: Env values must be JSON values.
func normalize(v any) any {
	switch x := v.(type) {
	case nil, bool, float64, string, []any, map[string]any:
		return v
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case float32:
		return float64(x)
	}
	return nil
}

func toNum(v any) (float64, bool) {
	switch x := normalize(v).(type) {
	case float64:
		return x, !math.IsNaN(x) && !math.IsInf(x, 0)
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case string:
		if len(x) > 64 {
			return 0, false
		}
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil && !math.IsNaN(f) && !math.IsInf(f, 0)
	}
	return 0, false
}

// equal compares two JSON values with no budget. The patch test operation
// uses it; the state limit bounds its work.
func equal(a, b any) bool {
	eq, _ := equalB(a, b, nil)
	return eq
}

// equalB compares two JSON values, charging a step per value and per 32
// bytes of string it compares.
func equalB(a, b any, bud *Budget) (bool, error) {
	if bud != nil {
		if err := bud.spend(1); err != nil {
			return false, err
		}
	}
	a, b = normalize(a), normalize(b)
	switch x := a.(type) {
	case nil:
		return b == nil, nil
	case bool, float64:
		return a == b, nil
	case string:
		y, ok := b.(string)
		if !ok {
			return false, nil
		}
		if bud != nil {
			if err := bud.spendBytes(min(len(x), len(y))); err != nil {
				return false, err
			}
		}
		return x == y, nil
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return false, nil
		}
		for i := range x {
			if eq, err := equalB(x[i], y[i], bud); err != nil || !eq {
				return false, err
			}
		}
		return true, nil
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok || len(x) != len(y) {
			return false, nil
		}
		for k, v := range x {
			w, ok := y[k]
			if !ok {
				return false, nil
			}
			if eq, err := equalB(v, w, bud); err != nil || !eq {
				return false, err
			}
		}
		return true, nil
	}
	return false, nil
}

func compareB(a, b any, bud *Budget) (int, bool, error) {
	a, b = normalize(a), normalize(b)
	if x, ok := a.(float64); ok {
		if y, ok := b.(float64); ok {
			switch {
			case x < y:
				return -1, true, nil
			case x > y:
				return 1, true, nil
			}
			return 0, true, nil
		}
	}
	if x, ok := a.(string); ok {
		if y, ok := b.(string); ok {
			if err := bud.spendBytes(min(len(x), len(y))); err != nil {
				return 0, false, err
			}
			return strings.Compare(x, y), true, nil
		}
	}
	return 0, false, nil
}

// Stringify formats a value as text: null is empty, a whole number has no
// decimals, and an array or object is JSON with sorted keys. The result
// stops at 64 KiB.
func Stringify(v any) string {
	var b strings.Builder
	_ = writeText(&b, v, nil)
	return cutRunes(b.String(), maxString)
}

// str formats a value as Stringify does, charging the budget for each value
// and each 32 bytes. ok is false when the text would pass maxString.
func str(v any, bud *Budget) (string, bool, error) {
	var b strings.Builder
	switch err := writeText(&b, v, bud); err {
	case nil:
		return b.String(), true, nil
	case errTooLong:
		return "", false, nil
	default:
		return "", false, err
	}
}

func writeText(b *strings.Builder, v any, bud *Budget) error {
	switch x := normalize(v).(type) {
	case nil:
		return nil
	case string:
		if len(x) > maxString {
			b.WriteString(cutRunes(x, maxString))
			return errTooLong
		}
		if bud != nil {
			if err := bud.spendBytes(len(x)); err != nil {
				return err
			}
		}
		b.WriteString(x)
		return nil
	case bool:
		b.WriteString(strconv.FormatBool(x))
		return nil
	case float64:
		b.WriteString(formatNum(x))
		return nil
	}
	return writeJSON(b, v, bud)
}

// writeJSON writes v as compact JSON with sorted keys, as encoding/json
// does, and stops at maxString.
func writeJSON(b *strings.Builder, v any, bud *Budget) error {
	if bud != nil {
		if err := bud.spend(1); err != nil {
			return err
		}
	}
	if b.Len() > maxString {
		return errTooLong
	}
	switch x := normalize(v).(type) {
	case nil:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(x))
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			b.WriteString("null")
		} else {
			b.WriteString(formatNum(x))
		}
	case string:
		if len(x) > maxString {
			return errTooLong
		}
		if bud != nil {
			if err := bud.spendBytes(len(x)); err != nil {
				return err
			}
		}
		writeQuoted(b, x)
	case []any:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeJSON(b, e, bud); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		if bud != nil {
			if err := bud.spend(len(keys)); err != nil {
				return err
			}
		}
		slices.Sort(keys)
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			writeQuoted(b, k)
			b.WriteByte(':')
			if err := writeJSON(b, x[k], bud); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	default:
		b.WriteString("null")
	}
	if b.Len() > maxString {
		return errTooLong
	}
	return nil
}

const hexDigits = "0123456789abcdef"

func writeQuoted(b *strings.Builder, s string) {
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"' || c == '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\t':
			b.WriteString(`\t`)
		case c < 0x20:
			b.WriteString(`\u00`)
			b.WriteByte(hexDigits[c>>4])
			b.WriteByte(hexDigits[c&15])
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
}

// cutRunes cuts s to at most n bytes at a rune boundary.
func cutRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

func formatNum(f float64) string {
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

var funcNames = [...]string{
	"abs", "ago", "avg", "bytes", "contains", "count", "dur", "fmt", "format",
	"join", "len", "lower", "max", "min", "pct", "pick", "round", "starts",
	"sum", "trim", "upper",
}

// Funcs lists the functions an expression may call, sorted.
func Funcs() []string { return slices.Clone(funcNames[:]) }

func isFunc(name string) bool { return slices.Contains(funcNames[:], name) }

func arg(a []any, i int) any {
	if i < len(a) {
		return normalize(a[i])
	}
	return nil
}

// call runs one function. An unknown function gives null.
func call(name string, a []any, env *Env) (any, error) {
	bud := env.Budget
	switch name {
	case "len":
		switch x := arg(a, 0).(type) {
		case string:
			if err := bud.spendBytes(len(x)); err != nil {
				return nil, err
			}
			return float64(utf8.RuneCountInString(x)), nil
		case []any:
			return float64(len(x)), nil
		case map[string]any:
			return float64(len(x)), nil
		}
		return 0.0, nil
	case "sum", "avg":
		nums, err := numbers(a, bud)
		if err != nil {
			return nil, err
		}
		if name == "avg" && len(nums) == 0 {
			return nil, nil
		}
		var s float64
		for _, n := range nums {
			s += n
		}
		if name == "avg" {
			s /= float64(len(nums))
		}
		return s, nil
	case "min", "max":
		nums, err := numbers(a, bud)
		if err != nil || len(nums) == 0 {
			return nil, err
		}
		if name == "min" {
			return slices.Min(nums), nil
		}
		return slices.Max(nums), nil
	case "round":
		f, ok := toNum(arg(a, 0))
		if !ok {
			return nil, nil
		}
		d, _ := toNum(arg(a, 1))
		p := math.Pow(10, math.Trunc(math.Max(0, math.Min(d, 10))))
		return math.Round(f*p) / p, nil
	case "abs":
		if f, ok := toNum(arg(a, 0)); ok {
			return math.Abs(f), nil
		}
		return nil, nil
	case "pct":
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
	case "fmt", "format":
		return fnFmt(a, bud)
	case "dur":
		f, ok := toNum(arg(a, 0))
		if !ok {
			return nil, nil
		}
		unit, _ := arg(a, 1).(string)
		return formatDur(f, unit), nil
	case "bytes":
		if f, ok := toNum(arg(a, 0)); ok {
			return formatBytes(f), nil
		}
		return nil, nil
	case "ago":
		t, ok := toTime(arg(a, 0))
		if !ok {
			return nil, nil
		}
		now := time.Now()
		if env.Now != nil {
			now = env.Now()
		}
		return formatAgo(now.Sub(t)), nil
	case "upper", "lower", "trim":
		s, ok := arg(a, 0).(string)
		if !ok || len(s) > maxString {
			return nil, nil
		}
		if err := bud.spendBytes(len(s)); err != nil {
			return nil, err
		}
		switch name {
		case "upper":
			return strings.ToUpper(s), nil
		case "lower":
			return strings.ToLower(s), nil
		}
		return strings.TrimSpace(s), nil
	case "join":
		arr, _ := arg(a, 0).([]any)
		sep := ", "
		if s, ok := arg(a, 1).(string); ok && len(s) <= 256 {
			sep = s
		}
		var b strings.Builder
		if err := bud.spend(len(arr)); err != nil {
			return nil, err
		}
		for i, v := range arr {
			if i > 0 {
				b.WriteString(sep)
			}
			if err := writeText(&b, v, bud); err != nil {
				if err == errTooLong {
					return nil, nil
				}
				return nil, err
			}
			if b.Len() > maxString {
				return nil, nil
			}
		}
		return b.String(), nil
	case "contains":
		switch x := arg(a, 0).(type) {
		case string:
			s, ok := arg(a, 1).(string)
			if !ok {
				return false, nil
			}
			if err := bud.spendBytes(len(x)); err != nil {
				return nil, err
			}
			return strings.Contains(x, s), nil
		case []any:
			for _, v := range x {
				eq, err := equalB(v, arg(a, 1), bud)
				if err != nil || eq {
					return eq, err
				}
			}
		}
		return false, nil
	case "starts":
		s, ok1 := arg(a, 0).(string)
		p, ok2 := arg(a, 1).(string)
		if !ok1 || !ok2 {
			return false, nil
		}
		if err := bud.spendBytes(len(p)); err != nil {
			return nil, err
		}
		return strings.HasPrefix(s, p), nil
	case "pick":
		arr, _ := arg(a, 0).([]any)
		f, _ := arg(a, 1).(string)
		if err := bud.spend(len(arr)); err != nil {
			return nil, err
		}
		out := make([]any, len(arr))
		for i, v := range arr {
			if m, ok := v.(map[string]any); ok {
				out[i] = normalize(m[f])
			}
		}
		return out, nil
	case "count":
		arr, _ := arg(a, 0).([]any)
		if err := bud.spend(len(arr)); err != nil {
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
			if len(a) < 3 {
				if Truthy(m[f]) {
					n++
				}
				continue
			}
			eq, err := equalB(m[f], a[2], bud)
			if err != nil {
				return nil, err
			}
			if eq {
				n++
			}
		}
		return float64(n), nil
	}
	return nil, nil
}

// numbers reads the numbers of a call: one array, optionally with a field
// name for an array of rows, or several numbers.
func numbers(a []any, bud *Budget) ([]float64, error) {
	var out []float64
	if arr, ok := arg(a, 0).([]any); ok {
		if err := bud.spend(len(arr)); err != nil {
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

// fnFmt formats a number with thousands separators and a fixed number of
// decimals: fmt(1234.5) is 1,234.5 and fmt(1234.5, 0) is 1,235. Any other
// value is formatted as Stringify does.
func fnFmt(a []any, bud *Budget) (any, error) {
	v := arg(a, 0)
	f, ok := v.(float64)
	if !ok {
		s, ok, err := str(v, bud)
		if err != nil || !ok {
			return nil, err
		}
		return s, nil
	}
	digits := -1
	if d, ok := toNum(arg(a, 1)); ok {
		digits = int(math.Max(0, math.Min(d, 10)))
		p := math.Pow(10, float64(digits))
		f = math.Round(f*p) / p // half away from zero, as people round
	}
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return nil, nil
	}
	s := strconv.FormatFloat(math.Abs(f), 'f', digits, 64)
	intPart, frac, _ := strings.Cut(s, ".")
	var b strings.Builder
	if f < 0 && strings.Trim(s, "0.") != "" {
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

func durScale(unit string) float64 {
	switch unit {
	case "ns":
		return 1e-9
	case "us", "µs":
		return 1e-6
	case "s":
		return 1
	case "m":
		return 60
	case "h":
		return 3600
	}
	return 1e-3
}

// formatDur formats an amount of unit (default ms) as a short duration:
// 120µs, 840ms, 2.21s, 4m 5s, 1h 4m. A duration too large to count gives
// null.
func formatDur(v float64, unit string) any {
	s := v * durScale(unit)
	if math.IsNaN(s) || math.IsInf(s, 0) || math.Abs(s) > 1e15 {
		return nil
	}
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
	const units = "BKMGTPE"
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
		return sign + strconv.FormatFloat(math.Round(v), 'f', 0, 64) + units[i:i+1]
	}
	return sign + trimZeros(strconv.FormatFloat(v, 'f', 1, 64)) + units[i:i+1]
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
		if len(x) > 64 {
			return time.Time{}, false
		}
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
