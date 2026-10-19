// Ported from pele src/diagrams/common/generics.ts at 458322a5b8abb1ae57ddf127c32017ab9fb14a92.

package common

import "strings"

func tildes(text string) int {
	return strings.Count(text, "~")
}

// Pairs tildes from the outside in: the first with the last, the second with the one before it.
func pairTildes(text string) string {
	count := tildes(text)
	if count <= 1 {
		return text
	}
	lead := count%2 != 0 && strings.HasPrefix(text, "~")
	chars := []byte(text)
	var at []int
	i := 0
	if lead {
		i = 1
	}
	for ; i < len(chars); i++ {
		if chars[i] == '~' {
			at = append(at, i)
		}
	}
	for lo, hi := 0, len(at)-1; lo < hi; lo, hi = lo+1, hi-1 {
		chars[at[lo]] = '<'
		chars[at[hi]] = '>'
	}
	return string(chars)
}

// ParseGenericTypes turns `List~int~` into `List<int>`, with the quirks of Mermaid's parseGenericTypes around commas.
func ParseGenericTypes(input string) string {
	if !strings.Contains(input, "~") {
		return input
	}
	// input.split(/(,)/): the parts with the commas kept between them.
	parts := strings.Split(input, ",")
	sets := make([]string, 0, 2*len(parts)-1)
	for k, part := range parts {
		if k > 0 {
			sets = append(sets, ",")
		}
		sets = append(sets, part)
	}
	output := make([]string, 0, len(sets))
	for i := 0; i < len(sets); i++ {
		set := sets[i]
		if set == "," && i > 0 && i+1 < len(sets) && tildes(sets[i-1]) == 1 && tildes(sets[i+1]) == 1 {
			set = sets[i-1] + "," + sets[i+1]
			i++
			output = output[:len(output)-1]
		}
		output = append(output, pairTildes(set))
	}
	return strings.Join(output, "")
}
