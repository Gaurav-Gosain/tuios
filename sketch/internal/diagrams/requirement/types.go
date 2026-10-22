// Ported from pele src/diagrams/requirement/types.ts at 8fad23bd51783e4df1204e177a7fb0d2a30dcd13.

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
