// Ported from pele src/diagrams/sequence/colors.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.

package sequence

import (
	"regexp"
	"strings"
	"sync"

	"tuios.dev/sketch/internal/js"
)

// Mermaid asks the browser whether the first word of a box title is a color. Pele has no browser,
// so it knows the color keywords and the rgb() and hsl() notations. Other functions, such as calc()
// and var(), are not recognized.
const KEYWORDS = "aliceblue antiquewhite aqua aquamarine azure beige bisque black blanchedalmond blue blueviolet brown burlywood cadetblue chartreuse chocolate coral cornflowerblue cornsilk crimson cyan darkblue darkcyan darkgoldenrod darkgray darkgreen darkgrey darkkhaki darkmagenta darkolivegreen darkorange darkorchid darkred darksalmon darkseagreen darkslateblue darkslategray darkslategrey darkturquoise darkviolet deeppink deepskyblue dimgray dimgrey dodgerblue firebrick floralwhite forestgreen fuchsia gainsboro ghostwhite gold goldenrod gray green greenyellow grey honeydew hotpink indianred indigo ivory khaki lavender lavenderblush lawngreen lemonchiffon lightblue lightcoral lightcyan lightgoldenrodyellow lightgray lightgreen lightgrey lightpink lightsalmon lightseagreen lightskyblue lightslategray lightslategrey lightsteelblue lightyellow lime limegreen linen magenta maroon mediumaquamarine mediumblue mediumorchid mediumpurple mediumseagreen mediumslateblue mediumspringgreen mediumturquoise mediumvioletred midnightblue mintcream mistyrose moccasin navajowhite navy oldlace olive olivedrab orange orangered orchid palegoldenrod palegreen paleturquoise palevioletred papayawhip peachpuff peru pink plum powderblue purple rebeccapurple red rosybrown royalblue saddlebrown salmon sandybrown seagreen seashell sienna silver skyblue slateblue slategray slategrey snow springgreen steelblue tan teal thistle tomato turquoise violet wheat white whitesmoke yellow yellowgreen transparent currentcolor inherit initial unset revert accentcolor accentcolortext activetext buttonborder buttonface buttontext canvas canvastext field fieldtext graytext highlight highlighttext linktext mark marktext selecteditem selecteditemtext visitedtext activeborder activecaption appworkspace background buttonhighlight buttonshadow captiontext inactiveborder inactivecaption inactivecaptiontext infobackground infotext menu menutext scrollbar threeddarkshadow threedface threedhighlight threedlightshadow threedshadow window windowframe windowtext"

var (
	keywordsOnce sync.Once
	keywords     map[string]bool
)

var reTerm = regexp.MustCompile(`(?i)^[+-]?(?:\d+(?:\.\d+)?|\.\d+)(?:e[+-]?\d+)?(%|deg|grad|rad|turn)?$`)

const (
	kBad = iota
	kNumber
	kPercent
	kAngle
	kNone
)

func kind(term string) int {
	if strings.ToLower(term) == "none" {
		return kNone
	}
	m := reTerm.FindStringSubmatch(term)
	if m == nil {
		return kBad
	}
	switch m[1] {
	case "":
		return kNumber
	case "%":
		return kPercent
	}
	return kAngle
}

func isAlpha(term string, modern bool) bool {
	k := kind(term)
	return k == kNumber || k == kPercent || (modern && k == kNone)
}

var reSpaces = regexp.MustCompile(`[` + js.S + `]+`)

func isFunction(color string) bool {
	hsl := strings.HasPrefix(color, "hsl")
	open := strings.IndexByte(color, '(')
	name := color[:open]
	if name != "rgb" && name != "rgba" && name != "hsl" && name != "hsla" {
		return false
	}
	if js.Len(color) > 120 || strings.IndexByte(color, ')') != len(color)-1 {
		return false
	}
	inner := js.Trim(color[open+1 : len(color)-1])
	var parts []string
	var alpha *string
	legacy := strings.Contains(inner, ",")
	if legacy {
		for _, s := range strings.Split(inner, ",") {
			parts = append(parts, js.Trim(s))
		}
		if len(parts) == 4 {
			a := parts[3]
			alpha = &a
			parts = parts[:3]
		}
	} else {
		slash := strings.Split(inner, "/")
		if len(slash) > 2 {
			return false
		}
		parts = reSpaces.Split(js.Trim(slash[0]), -1)
		if len(slash) == 2 {
			a := js.Trim(slash[1])
			alpha = &a
		}
	}
	if len(parts) != 3 || (alpha != nil && !isAlpha(*alpha, !legacy)) {
		return false
	}
	kinds := []int{kind(parts[0]), kind(parts[1]), kind(parts[2])}
	if legacy {
		if hsl {
			return (kinds[0] == kNumber || kinds[0] == kAngle) && kinds[1] == kPercent && kinds[2] == kPercent
		}
		return (kinds[0] == kNumber || kinds[0] == kPercent) && kinds[1] == kinds[0] && kinds[2] == kinds[0]
	}
	for i := 0; i < 3; i++ {
		k := kinds[i]
		var ok bool
		if k == kNumber || k == kNone {
			ok = true
		} else if hsl && i == 0 {
			ok = k == kAngle
		} else {
			ok = k == kPercent
		}
		if !ok {
			return false
		}
	}
	return true
}

func isCssColor(color string) bool {
	if strings.Contains(color, "(") {
		return isFunction(color)
	}
	keywordsOnce.Do(func() {
		keywords = map[string]bool{}
		for _, k := range strings.Split(KEYWORDS, " ") {
			keywords[k] = true
		}
	})
	return keywords[strings.ToLower(color)]
}
