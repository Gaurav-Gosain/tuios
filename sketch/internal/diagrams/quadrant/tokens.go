// Ported from pele src/diagrams/quadrant/tokens.ts at 8aff183be57021b533f319a29341a529be934b08.

package quadrant

// Token types.
const (
	T_END = iota
	T_ERROR
	T_NEWLINE
	T_title
	T_title_value
	T_acc_title
	T_acc_title_value
	T_acc_descr
	T_acc_descr_value
	T_acc_descr_multiline_value
	T_X_AXIS
	T_Y_AXIS
	T_DELIMITER
	T_QUADRANT_1
	T_QUADRANT_2
	T_QUADRANT_3
	T_QUADRANT_4
	T_CLASSDEF
	T_MD_STR
	T_STR
	T_class_name
	T_point_start
	T_point_x
	T_point_y
	T_QUADRANT
	T_ALPHA
	T_UNICODE_TEXT
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
	T_SPACE
	T_SEMI
	T_PUNCTUATION
	T_EOF
)

var TOKEN_NAMES = [...]string{
	"$end",
	"INVALID",
	"NEWLINE",
	"title",
	"title_value",
	"acc_title",
	"acc_title_value",
	"acc_descr",
	"acc_descr_value",
	"acc_descr_multiline_value",
	"X-AXIS",
	"Y-AXIS",
	"AXIS-TEXT-DELIMITER",
	"QUADRANT_1",
	"QUADRANT_2",
	"QUADRANT_3",
	"QUADRANT_4",
	"CLASSDEF",
	"MD_STR",
	"STR",
	"class_name",
	"point_start",
	"point_x",
	"point_y",
	"QUADRANT",
	"ALPHA",
	"UNICODE_TEXT",
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
	"SPACE",
	"SEMI",
	"PUNCTUATION",
	"EOF",
}
