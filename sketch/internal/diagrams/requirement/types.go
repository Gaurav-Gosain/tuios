// Ported from pele src/diagrams/requirement/types.ts at 3a2f075ee0c3d914f0fad3545fc0f193693975ac.

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
