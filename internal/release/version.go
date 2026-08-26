package release

import (
	"strconv"
	"strings"
)

// Comparing two tuios versions.
//
// Deliberately small. The tags this has to order are v0.7.0 and v0.7.1-rc1, and
// a full semver implementation would be a dependency and a lot of rules for
// build metadata that these tags never carry. Anything this cannot parse is
// reported as such rather than guessed at, and the command then says "this is
// not a release build" instead of comparing nonsense.

// ParsedVersion is a tag broken into its parts.
type ParsedVersion struct {
	Major, Minor, Patch int
	// Pre is the prerelease suffix without its dash, "rc1", empty for a final
	// release.
	Pre string
}

// ParseVersion reads a tag like "v0.7.1" or "0.7.1-rc1".
//
// A version with fewer than three numbers is accepted with the missing ones
// zero, since "v1" and "v1.0" are both tags a human writes. Anything with a
// non-numeric component is not a release tag: "dev+abc123" is what a source
// build reports, and treating it as a version is how a source build ends up
// being told it is out of date.
func ParseVersion(v string) (ParsedVersion, bool) {
	v = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(v), "v"))
	if v == "" {
		return ParsedVersion{}, false
	}
	core, pre, _ := strings.Cut(v, "-")
	// Build metadata is dropped rather than compared, which is what semver
	// itself says to do with it.
	core, _, _ = strings.Cut(core, "+")

	parts := strings.Split(core, ".")
	if len(parts) > 3 {
		return ParsedVersion{}, false
	}
	var out ParsedVersion
	nums := []*int{&out.Major, &out.Minor, &out.Patch}
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return ParsedVersion{}, false
		}
		*nums[i] = n
	}
	out.Pre = pre
	return out, true
}

// Compare orders two parsed versions: -1, 0 or 1.
//
// A prerelease sorts below the release it leads to, which is the rule that
// stops v0.8.0-rc1 being offered as an upgrade from v0.8.0.
func Compare(a, b ParsedVersion) int {
	for _, pair := range [][2]int{{a.Major, b.Major}, {a.Minor, b.Minor}, {a.Patch, b.Patch}} {
		if pair[0] != pair[1] {
			if pair[0] < pair[1] {
				return -1
			}
			return 1
		}
	}
	switch {
	case a.Pre == b.Pre:
		return 0
	case a.Pre == "":
		return 1 // a is the final release, b is a prerelease of it
	case b.Pre == "":
		return -1
	case a.Pre < b.Pre:
		return -1
	}
	return 1
}

// Newer reports whether candidate is a later version than current, and whether
// both could be read at all. A version that cannot be parsed makes the
// comparison meaningless, and saying so is the point of the second return.
func Newer(current, candidate string) (newer, comparable bool) {
	cur, okCur := ParseVersion(current)
	cand, okCand := ParseVersion(candidate)
	if !okCur || !okCand {
		return false, false
	}
	return Compare(cand, cur) > 0, true
}
