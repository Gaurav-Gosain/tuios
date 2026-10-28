// Ported from pele src/diagrams/block/tokens.ts at 8aff183be57021b533f319a29341a529be934b08.

package block

// Token types.
const (
	T_END = iota
	T_ERROR
	T_KEY
	T_ID_BLOCK
	T_COLUMNS
	T_MD_STR
	T_STR
	T_SPACE_BLOCK
	T_DEFAULT
	T_LINKSTYLE
	T_INTERPOLATE
	T_CLASSDEF
	T_DEFAULT_CLASSDEF_ID
	T_CLASSDEF_ID
	T_CLASSDEF_STYLEOPTS
	T_CLASS
	T_CLASSENTITY_IDS
	T_STYLECLASS
	T_STYLE
	T_STYLE_ENTITY_IDS
	T_STYLE_DEFINITION_DATA
	T_acc_title
	T_acc_title_value
	T_acc_descr
	T_acc_descr_value
	T_acc_descr_multiline_value
	T_end
	T_NODE_DSTART
	T_NODE_DEND
	T_BLOCK_ARROW_START
	T_BLOCK_ARROW_END
	T_DIR
	T_NODE_ID
	T_LINK
	T_START_LINK
	T_LINK_LABEL
	T_SIZE
	T_EOF
)

var TOKEN_NAMES = [...]string{
	"$end",
	"INVALID",
	"BLOCK_DIAGRAM_KEY",
	"id-block",
	"COLUMNS",
	"MD_STR",
	"STR",
	"SPACE_BLOCK",
	"DEFAULT",
	"LINKSTYLE",
	"INTERPOLATE",
	"classDef",
	"DEFAULT_CLASSDEF_ID",
	"CLASSDEF_ID",
	"CLASSDEF_STYLEOPTS",
	"class",
	"CLASSENTITY_IDS",
	"STYLECLASS",
	"style",
	"STYLE_ENTITY_IDS",
	"STYLE_DEFINITION_DATA",
	"acc_title",
	"acc_title_value",
	"acc_descr",
	"acc_descr_value",
	"acc_descr_multiline_value",
	"end",
	"NODE_DSTART",
	"NODE_DEND",
	"BLOCK_ARROW_START",
	"BLOCK_ARROW_END",
	"DIR",
	"NODE_ID",
	"LINK",
	"START_LINK",
	"LINK_LABEL",
	"SIZE",
	"EOF",
}
