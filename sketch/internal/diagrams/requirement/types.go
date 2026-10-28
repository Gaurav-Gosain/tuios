// Ported from pele src/diagrams/requirement/types.ts at 8aff183be57021b533f319a29341a529be934b08.

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
