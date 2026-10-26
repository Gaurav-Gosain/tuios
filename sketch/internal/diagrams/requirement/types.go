// Ported from pele src/diagrams/requirement/types.ts at 4eff76f98c73b6817800a05773517f35832e4e92.

package requirement

type Requirement struct {
	name          string
	typ           string
	requirementId string
	text          string
	risk          string
	verifyMethod  string
	cssStyles     []string
	classes       []string
}

type RequirementElement struct {
	name      string
	typ       string
	docRef    string
	cssStyles []string
	classes   []string
}

type Relation struct {
	typ string
	src string
	dst string
}

type RequirementClass struct {
	id         string
	styles     []string
	textStyles []string
}
