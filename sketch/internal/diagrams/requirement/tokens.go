// Ported from pele src/diagrams/requirement/tokens.ts at 8aff183be57021b533f319a29341a529be934b08.

package requirement

// Token types.
const (
	T_END = iota
	T_ERROR
	T_title
	T_acc_title
	T_acc_title_value
	T_acc_descr
	T_acc_descr_value
	T_acc_descr_multiline_value
	T_direction_tb
	T_direction_bt
	T_direction_rl
	T_direction_lr
	T_NEWLINE
	T_EOF
	T_RD
	T_STRUCT_START
	T_STRUCT_STOP
	T_STYLE_SEPARATOR
	T_COLONSEP
	T_ID
	T_TEXT
	T_RISK
	T_VERIFYMTHD
	T_REQUIREMENT
	T_FUNCTIONAL_REQUIREMENT
	T_INTERFACE_REQUIREMENT
	T_PERFORMANCE_REQUIREMENT
	T_PHYSICAL_REQUIREMENT
	T_DESIGN_CONSTRAINT
	T_LOW_RISK
	T_MED_RISK
	T_HIGH_RISK
	T_VERIFY_ANALYSIS
	T_VERIFY_DEMONSTRATION
	T_VERIFY_INSPECTION
	T_VERIFY_TEST
	T_ELEMENT
	T_CONTAINS
	T_COPIES
	T_DERIVES
	T_SATISFIES
	T_VERIFIES
	T_REFINES
	T_TRACES
	T_TYPE
	T_DOCREF
	T_STYLE
	T_CLASSDEF
	T_CLASS
	T_ALPHA
	T_COLON
	T_SEMICOLON
	T_PERCENT
	T_MINUS
	T_BRKT
	T_END_ARROW_L
	T_END_ARROW_R
	T_LINE
	T_qString
	T_unqString
	T_COMMA
)

var TOKEN_NAMES = [...]string{
	"$end",
	"INVALID",
	"title",
	"acc_title",
	"acc_title_value",
	"acc_descr",
	"acc_descr_value",
	"acc_descr_multiline_value",
	"direction_tb",
	"direction_bt",
	"direction_rl",
	"direction_lr",
	"NEWLINE",
	"EOF",
	"RD",
	"STRUCT_START",
	"STRUCT_STOP",
	"STYLE_SEPARATOR",
	"COLONSEP",
	"ID",
	"TEXT",
	"RISK",
	"VERIFYMTHD",
	"REQUIREMENT",
	"FUNCTIONAL_REQUIREMENT",
	"INTERFACE_REQUIREMENT",
	"PERFORMANCE_REQUIREMENT",
	"PHYSICAL_REQUIREMENT",
	"DESIGN_CONSTRAINT",
	"LOW_RISK",
	"MED_RISK",
	"HIGH_RISK",
	"VERIFY_ANALYSIS",
	"VERIFY_DEMONSTRATION",
	"VERIFY_INSPECTION",
	"VERIFY_TEST",
	"ELEMENT",
	"CONTAINS",
	"COPIES",
	"DERIVES",
	"SATISFIES",
	"VERIFIES",
	"REFINES",
	"TRACES",
	"TYPE",
	"DOCREF",
	"STYLE",
	"CLASSDEF",
	"CLASS",
	"ALPHA",
	"COLON",
	"SEMICOLON",
	"PERCENT",
	"MINUS",
	"BRKT",
	"END_ARROW_L",
	"END_ARROW_R",
	"LINE",
	"qString",
	"unqString",
	"COMMA",
}

// keyword: every keyword is matched without regard to case and must end at a word boundary.
// It is a switch rather than a map, which would allocate at init. lower is in lower case.
func keyword(lower string) (int, bool) {
	switch lower {
	case "requirementdiagram":
		return T_RD, true
	case "id":
		return T_ID, true
	case "text":
		return T_TEXT, true
	case "risk":
		return T_RISK, true
	case "verifymethod":
		return T_VERIFYMTHD, true
	case "requirement":
		return T_REQUIREMENT, true
	case "functionalrequirement":
		return T_FUNCTIONAL_REQUIREMENT, true
	case "interfacerequirement":
		return T_INTERFACE_REQUIREMENT, true
	case "performancerequirement":
		return T_PERFORMANCE_REQUIREMENT, true
	case "physicalrequirement":
		return T_PHYSICAL_REQUIREMENT, true
	case "designconstraint":
		return T_DESIGN_CONSTRAINT, true
	case "low":
		return T_LOW_RISK, true
	case "medium":
		return T_MED_RISK, true
	case "high":
		return T_HIGH_RISK, true
	case "analysis":
		return T_VERIFY_ANALYSIS, true
	case "demonstration":
		return T_VERIFY_DEMONSTRATION, true
	case "inspection":
		return T_VERIFY_INSPECTION, true
	case "test":
		return T_VERIFY_TEST, true
	case "element":
		return T_ELEMENT, true
	case "contains":
		return T_CONTAINS, true
	case "copies":
		return T_COPIES, true
	case "derives":
		return T_DERIVES, true
	case "satisfies":
		return T_SATISFIES, true
	case "verifies":
		return T_VERIFIES, true
	case "refines":
		return T_REFINES, true
	case "traces":
		return T_TRACES, true
	case "type":
		return T_TYPE, true
	case "docref":
		return T_DOCREF, true
	case "style":
		return T_STYLE, true
	case "classdef":
		return T_CLASSDEF, true
	case "class":
		return T_CLASS, true
	}
	return 0, false
}
