// Ported from pele src/diagrams/er/tokens.ts at 8aff183be57021b533f319a29341a529be934b08.

// Package er parses and draws Mermaid entity relationship diagrams.
package er

// Token types. pele names them T.X; the port names them T_X.
const (
	T_END = iota
	T_ERROR
	T_ER_DIAGRAM
	T_EOF
	T_NEWLINE
	T_COLON
	T_STYLE_SEPARATOR
	T_BLOCK_START
	T_BLOCK_STOP
	T_SQS
	T_SQE
	T_acc_title
	T_acc_title_value
	T_acc_descr
	T_acc_descr_value
	T_acc_descr_multiline_value
	T_END_KW
	T_SUBGRAPH
	T_direction_tb
	T_direction_bt
	T_direction_rl
	T_direction_lr
	T_CLASSDEF
	T_UNICODE_TEXT
	T_STYLE_TEXT
	T_COMMA
	T_CLASS
	T_STYLE
	T_SEMI
	T_NUM
	T_BRKT
	T_ENTITY_NAME
	T_DECIMAL_NUM
	T_ENTITY_ONE
	T_ATTRIBUTE_WORD
	T_ATTRIBUTE_KEY
	T_COMMENT
	T_ZERO_OR_ONE
	T_ZERO_OR_MORE
	T_ONE_OR_MORE
	T_ONLY_ONE
	T_MD_PARENT
	T_NON_IDENTIFYING
	T_IDENTIFYING
	T_WORD
	// A character no rule names; the grammar gives it meaning only as `?` and `,` inside a block.
	T_CHAR
)

var TOKEN_NAMES = [...]string{
	"$end",
	"INVALID",
	"ER_DIAGRAM",
	"EOF",
	"NEWLINE",
	"COLON",
	"STYLE_SEPARATOR",
	"BLOCK_START",
	"BLOCK_STOP",
	"SQS",
	"SQE",
	"acc_title",
	"acc_title_value",
	"acc_descr",
	"acc_descr_value",
	"acc_descr_multiline_value",
	"END",
	"SUBGRAPH",
	"direction_tb",
	"direction_bt",
	"direction_rl",
	"direction_lr",
	"CLASSDEF",
	"UNICODE_TEXT",
	"STYLE_TEXT",
	"COMMA",
	"CLASS",
	"STYLE",
	"SEMI",
	"NUM",
	"BRKT",
	"ENTITY_NAME",
	"DECIMAL_NUM",
	"ENTITY_ONE",
	"ATTRIBUTE_WORD",
	"ATTRIBUTE_KEY",
	"COMMENT",
	"ZERO_OR_ONE",
	"ZERO_OR_MORE",
	"ONE_OR_MORE",
	"ONLY_ONE",
	"MD_PARENT",
	"NON_IDENTIFYING",
	"IDENTIFYING",
	"WORD",
	"CHAR",
}
