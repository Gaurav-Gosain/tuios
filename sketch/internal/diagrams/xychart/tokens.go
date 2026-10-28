// Ported from pele src/diagrams/xychart/tokens.ts at 8aff183be57021b533f319a29341a529be934b08.

package xychart

// Token types.
const (
	T_END = iota
	T_ERROR
	T_NEWLINE
	T_title
	T_acc_title
	T_acc_title_value
	T_acc_descr
	T_acc_descr_value
	T_acc_descr_multiline_value
	T_XYCHART
	T_CHART_ORIENTATION
	T_X_AXIS
	T_Y_AXIS
	T_ARROW_DELIMITER
	T_LINE
	T_BAR
	T_NUMBER_WITH_DECIMAL
	T_SQUARE_BRACES_START
	T_SQUARE_BRACES_END
	T_STR
	T_ALPHA
	T_COLON
	T_PLUS
	T_COMMA
	T_EQUALS
	T_MULT
	T_BRKT
	T_UNDERSCORE
	T_DOT
	T_AMP
	T_MINUS
	T_NUM
	T_SEMI
	T_EOF
)

var TOKEN_NAMES = [...]string{
	"$end",
	"INVALID",
	"NEWLINE",
	"title",
	"acc_title",
	"acc_title_value",
	"acc_descr",
	"acc_descr_value",
	"acc_descr_multiline_value",
	"XYCHART",
	"CHART_ORIENTATION",
	"X_AXIS",
	"Y_AXIS",
	"ARROW_DELIMITER",
	"LINE",
	"BAR",
	"NUMBER_WITH_DECIMAL",
	"SQUARE_BRACES_START",
	"SQUARE_BRACES_END",
	"STR",
	"ALPHA",
	"COLON",
	"PLUS",
	"COMMA",
	"EQUALS",
	"MULT",
	"BRKT",
	"UNDERSCORE",
	"DOT",
	"AMP",
	"MINUS",
	"NUM",
	"SEMI",
	"EOF",
}
