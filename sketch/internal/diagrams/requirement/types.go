// Ported from pele src/diagrams/requirement/types.ts at e7f092a4de147cc32210913c20e36eaa5fcd0ef1.

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
