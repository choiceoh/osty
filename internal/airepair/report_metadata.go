package airepair

import "github.com/osty/osty/internal/repair"

type reportChangeMeta struct {
	phase       string
	sourceHabit string
	confidence  float64
}

func annotateReportChange(change repair.Change) ReportChange {
	meta := reportChangeMetaForKind(change.Kind)
	return ReportChange{
		Kind:        change.Kind,
		Message:     change.Message,
		Pos:         change.Pos,
		Phase:       meta.phase,
		SourceHabit: meta.sourceHabit,
		Confidence:  meta.confidence,
	}
}

func reportChangeMetaForKind(kind string) reportChangeMeta {
	switch kind {
	case "fat_arrow":
		return reportChangeMeta{"lexical", "foreign_match_arrow", 0.99}
	case "uppercase_base_prefix":
		return reportChangeMeta{"lexical", "uppercase_numeric_base_prefix", 0.99}
	case "member_separator":
		return reportChangeMeta{"lexical", "rust_path_separator", 0.99}
	case "function_keyword":
		return reportChangeMeta{"lexical", "foreign_function_keyword", 0.99}
	case "const_keyword":
		return reportChangeMeta{"lexical", "const_declaration_keyword", 0.98}
	case "var_keyword":
		return reportChangeMeta{"lexical", "mutable_var_declaration_keyword", 0.98}
	case "visibility_keyword":
		return reportChangeMeta{"lexical", "visibility_keyword_spelling", 0.98}
	case "else_if_keyword":
		return reportChangeMeta{"lexical", "alternate_else_if_spelling", 0.98}
	case "while_keyword":
		return reportChangeMeta{"lexical", "while_condition_loop", 0.98}
	case "switch_keyword":
		return reportChangeMeta{"lexical", "switch_match_keyword", 0.98}
	case "case_arm":
		return reportChangeMeta{"lexical", "case_arm_colon", 0.97}
	case "default_arm":
		return reportChangeMeta{"lexical", "default_arm_colon", 0.97}
	case "value_spelling":
		return reportChangeMeta{"lexical", "foreign_literal_spelling", 0.99}
	case "word_operator":
		return reportChangeMeta{"lexical", "word_operator_spelling", 0.98}
	case "short_var_decl":
		return reportChangeMeta{"lexical", "go_short_var_decl", 0.99}
	case "console_log":
		return reportChangeMeta{"lexical", "javascript_console_log", 0.99}
	case "semicolon":
		return reportChangeMeta{"lexical", "statement_semicolon", 0.95}
	case "else_newline":
		return reportChangeMeta{"lexical", "newline_else_layout", 0.95}
	case "line_endings":
		return reportChangeMeta{"lexical", "crlf_line_endings", 1.00}
	case "strict_equality":
		return reportChangeMeta{"structural", "javascript_strict_equality", 0.99}
	case "strict_inequality":
		return reportChangeMeta{"structural", "javascript_strict_inequality", 0.99}
	case "import_keyword":
		return reportChangeMeta{"structural", "import_keyword", 0.98}
	case "from_import":
		return reportChangeMeta{"structural", "python_from_import", 0.99}
	case "python_else_block":
		return reportChangeMeta{"diagnostic", "python_else_colon_block", 0.97}
	case "python_elif_block":
		return reportChangeMeta{"diagnostic", "python_elif_colon_block", 0.97}
	case "python_else_if_block":
		return reportChangeMeta{"diagnostic", "python_else_if_colon_block", 0.97}
	case "python_if_block":
		return reportChangeMeta{"diagnostic", "python_if_colon_block", 0.97}
	case "python_for_block":
		return reportChangeMeta{"diagnostic", "python_for_colon_block", 0.97}
	case "python_while_block":
		return reportChangeMeta{"diagnostic", "python_while_colon_block", 0.97}
	case "python_match_block":
		return reportChangeMeta{"diagnostic", "python_match_colon_block", 0.97}
	case "python_case_arm":
		return reportChangeMeta{"diagnostic", "python_case_arm", 0.97}
	case "python_default_arm":
		return reportChangeMeta{"diagnostic", "python_default_arm", 0.97}
	case "python_arrow_arm_block":
		return reportChangeMeta{"diagnostic", "python_multiline_match_arm", 0.96}
	case "python_fn_block":
		return reportChangeMeta{"diagnostic", "python_function_colon_block", 0.97}
	case "python_struct_block":
		return reportChangeMeta{"diagnostic", "python_struct_colon_block", 0.96}
	case "python_interface_block":
		return reportChangeMeta{"diagnostic", "python_interface_colon_block", 0.96}
	case "js_for_of_loop":
		return reportChangeMeta{"loop", "javascript_for_of_loop", 0.98}
	case "python_range_loop":
		return reportChangeMeta{"loop", "python_range_loop", 0.97}
	case "python_enumerate_loop":
		return reportChangeMeta{"loop", "python_enumerate_loop", 0.97}
	case "tuple_loop_pattern":
		return reportChangeMeta{"loop", "bare_tuple_loop_binding", 0.96}
	case "enumerate_index_loop":
		return reportChangeMeta{"semantic", "python_enumerate_loop", 0.96}
	case "builtin_append_call":
		return reportChangeMeta{"semantic", "foreign_append_helper", 0.94}
	case "builtin_len_call":
		return reportChangeMeta{"semantic", "foreign_len_helper", 0.98}
	case "length_property":
		return reportChangeMeta{"semantic", "javascript_length_property", 0.98}
	default:
		return reportChangeMeta{"unknown", "unknown", 0.50}
	}
}
