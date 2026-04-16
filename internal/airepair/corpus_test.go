package airepair

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestAnalyzeCorpus(t *testing.T) {
	type corpusCase struct {
		name               string
		mode               Mode
		inputFile          string
		expectedFile       string
		wantChanged        bool
		wantImproved       bool
		wantAccepted       bool
		minBeforeParseErrs int
		minBeforeTotalErrs int
		wantAfterParseErrs int
		wantAfterTotalErrs int
		wantChangeKinds    []string
	}

	cases := []corpusCase{
		{
			name:               "already_clean",
			mode:               ModeFrontEndAssist,
			inputFile:          "already_clean.input.osty",
			expectedFile:       "already_clean.expected.osty",
			wantChanged:        false,
			wantImproved:       false,
			wantAccepted:       true,
			minBeforeParseErrs: 0,
			minBeforeTotalErrs: 0,
			wantAfterParseErrs: 0,
			wantAfterTotalErrs: 0,
			wantChangeKinds:    []string{},
		},
		{
			name:               "foreign_function_keyword",
			mode:               ModeFrontEndAssist,
			inputFile:          "foreign_function_keyword.input.osty",
			expectedFile:       "foreign_function_keyword.expected.osty",
			wantChanged:        true,
			wantImproved:       true,
			wantAccepted:       true,
			minBeforeParseErrs: 1,
			minBeforeTotalErrs: 1,
			wantAfterParseErrs: 0,
			wantAfterTotalErrs: 0,
			wantChangeKinds:    []string{"function_keyword"},
		},
		{
			name:               "go_short_decl",
			mode:               ModeFrontEndAssist,
			inputFile:          "go_short_decl.input.osty",
			expectedFile:       "go_short_decl.expected.osty",
			wantChanged:        true,
			wantImproved:       true,
			wantAccepted:       true,
			minBeforeParseErrs: 1,
			minBeforeTotalErrs: 1,
			wantAfterParseErrs: 0,
			wantAfterTotalErrs: 0,
			wantChangeKinds:    []string{"short_var_decl"},
		},
		{
			name:               "rewrite_console_log_nil",
			mode:               ModeRewriteOnly,
			inputFile:          "rewrite_console_log_nil.input.osty",
			expectedFile:       "rewrite_console_log_nil.expected.osty",
			wantChanged:        true,
			wantImproved:       true,
			wantAccepted:       true,
			minBeforeParseErrs: 0,
			minBeforeTotalErrs: 1,
			wantAfterParseErrs: 0,
			wantAfterTotalErrs: 0,
			wantChangeKinds:    []string{"console_log", "value_spelling"},
		},
		{
			name:               "js_strict_equality",
			mode:               ModeFrontEndAssist,
			inputFile:          "js_strict_equality.input.osty",
			expectedFile:       "js_strict_equality.expected.osty",
			wantChanged:        true,
			wantImproved:       true,
			wantAccepted:       true,
			minBeforeParseErrs: 1,
			minBeforeTotalErrs: 1,
			wantAfterParseErrs: 0,
			wantAfterTotalErrs: 0,
			wantChangeKinds:    []string{"strict_equality"},
		},
		{
			name:               "from_import",
			mode:               ModeFrontEndAssist,
			inputFile:          "from_import.input.osty",
			expectedFile:       "from_import.expected.osty",
			wantChanged:        true,
			wantImproved:       true,
			wantAccepted:       true,
			minBeforeParseErrs: 1,
			minBeforeTotalErrs: 1,
			wantAfterParseErrs: 0,
			wantAfterTotalErrs: 0,
			wantChangeKinds:    []string{"from_import"},
		},
		{
			name:               "python_elif",
			mode:               ModeFrontEndAssist,
			inputFile:          "python_elif.input.osty",
			expectedFile:       "python_elif.expected.osty",
			wantChanged:        true,
			wantImproved:       true,
			wantAccepted:       true,
			minBeforeParseErrs: 1,
			minBeforeTotalErrs: 1,
			wantAfterParseErrs: 0,
			wantAfterTotalErrs: 0,
			wantChangeKinds:    []string{"python_if_block", "python_elif_block", "python_else_block"},
		},
		{
			name:               "js_for_of_loop",
			mode:               ModeFrontEndAssist,
			inputFile:          "js_for_of_loop.input.osty",
			expectedFile:       "js_for_of_loop.expected.osty",
			wantChanged:        true,
			wantImproved:       true,
			wantAccepted:       true,
			minBeforeParseErrs: 1,
			minBeforeTotalErrs: 1,
			wantAfterParseErrs: 0,
			wantAfterTotalErrs: 0,
			wantChangeKinds:    []string{"js_for_of_loop"},
		},
		{
			name:               "js_destructuring_for_of_loop",
			mode:               ModeFrontEndAssist,
			inputFile:          "js_destructuring_for_of_loop.input.osty",
			expectedFile:       "js_destructuring_for_of_loop.expected.osty",
			wantChanged:        true,
			wantImproved:       true,
			wantAccepted:       true,
			minBeforeParseErrs: 1,
			minBeforeTotalErrs: 1,
			wantAfterParseErrs: 0,
			wantAfterTotalErrs: 0,
			wantChangeKinds:    []string{"js_for_of_loop"},
		},
		{
			name:               "python_range_loop",
			mode:               ModeFrontEndAssist,
			inputFile:          "python_range_loop.input.osty",
			expectedFile:       "python_range_loop.expected.osty",
			wantChanged:        true,
			wantImproved:       true,
			wantAccepted:       true,
			minBeforeParseErrs: 1,
			minBeforeTotalErrs: 1,
			wantAfterParseErrs: 0,
			wantAfterTotalErrs: 0,
			wantChangeKinds:    []string{"python_for_block", "python_range_loop"},
		},
		{
			name:               "python_range_loop_with_step",
			mode:               ModeFrontEndAssist,
			inputFile:          "python_range_loop_with_step.input.osty",
			expectedFile:       "python_range_loop_with_step.expected.osty",
			wantChanged:        true,
			wantImproved:       true,
			wantAccepted:       true,
			minBeforeParseErrs: 1,
			minBeforeTotalErrs: 1,
			wantAfterParseErrs: 0,
			wantAfterTotalErrs: 0,
			wantChangeKinds:    []string{"python_for_block", "python_range_loop"},
		},
		{
			name:               "python_enumerate_loop",
			mode:               ModeFrontEndAssist,
			inputFile:          "python_enumerate_loop.input.osty",
			expectedFile:       "python_enumerate_loop.expected.osty",
			wantChanged:        true,
			wantImproved:       true,
			wantAccepted:       true,
			minBeforeParseErrs: 1,
			minBeforeTotalErrs: 1,
			wantAfterParseErrs: 0,
			wantAfterTotalErrs: 0,
			wantChangeKinds:    []string{"python_for_block", "python_enumerate_loop", "enumerate_index_loop"},
		},
		{
			name:               "foreign_len_helpers",
			mode:               ModeFrontEndAssist,
			inputFile:          "foreign_len_helpers.input.osty",
			expectedFile:       "foreign_len_helpers.expected.osty",
			wantChanged:        true,
			wantImproved:       true,
			wantAccepted:       true,
			minBeforeParseErrs: 0,
			minBeforeTotalErrs: 1,
			wantAfterParseErrs: 0,
			wantAfterTotalErrs: 0,
			wantChangeKinds:    []string{"length_property", "builtin_len_call"},
		},
		{
			name:               "foreign_append_helper",
			mode:               ModeFrontEndAssist,
			inputFile:          "foreign_append_helper.input.osty",
			expectedFile:       "foreign_append_helper.expected.osty",
			wantChanged:        true,
			wantImproved:       true,
			wantAccepted:       true,
			minBeforeParseErrs: 0,
			minBeforeTotalErrs: 1,
			wantAfterParseErrs: 0,
			wantAfterTotalErrs: 0,
			wantChangeKinds:    []string{"builtin_append_call"},
		},
		{
			name:               "python_match_case",
			mode:               ModeFrontEndAssist,
			inputFile:          "python_match_case.input.osty",
			expectedFile:       "python_match_case.expected.osty",
			wantChanged:        true,
			wantImproved:       true,
			wantAccepted:       true,
			minBeforeParseErrs: 1,
			minBeforeTotalErrs: 1,
			wantAfterParseErrs: 0,
			wantAfterTotalErrs: 0,
			wantChangeKinds:    []string{"python_match_block", "python_case_arm", "python_default_arm"},
		},
		{
			name:               "python_bare_tuple_loop",
			mode:               ModeFrontEndAssist,
			inputFile:          "python_bare_tuple_loop.input.osty",
			expectedFile:       "python_bare_tuple_loop.expected.osty",
			wantChanged:        true,
			wantImproved:       true,
			wantAccepted:       true,
			minBeforeParseErrs: 1,
			minBeforeTotalErrs: 1,
			wantAfterParseErrs: 0,
			wantAfterTotalErrs: 0,
			wantChangeKinds:    []string{"python_for_block", "tuple_loop_pattern"},
		},
	}

	base := filepath.Join("testdata", "corpus")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := readCorpusFixture(t, filepath.Join(base, tc.inputFile))
			expected := readCorpusFixture(t, filepath.Join(base, tc.expectedFile))

			result := Analyze(Request{
				Source:   input,
				Filename: tc.inputFile,
				Mode:     tc.mode,
			})

			if result.Changed != tc.wantChanged {
				t.Fatalf("changed = %v, want %v", result.Changed, tc.wantChanged)
			}
			if result.Improved != tc.wantImproved {
				t.Fatalf("improved = %v, want %v", result.Improved, tc.wantImproved)
			}
			if result.Accepted != tc.wantAccepted {
				t.Fatalf("accepted = %v, want %v", result.Accepted, tc.wantAccepted)
			}
			if got := string(result.Repaired); got != string(expected) {
				t.Fatalf("repaired source = %q, want %q", got, string(expected))
			}
			if result.Before.Parse.Errors < tc.minBeforeParseErrs {
				t.Fatalf("before.parse.errors = %d, want at least %d", result.Before.Parse.Errors, tc.minBeforeParseErrs)
			}
			if result.Before.TotalErrors < tc.minBeforeTotalErrs {
				t.Fatalf("before.total_errors = %d, want at least %d", result.Before.TotalErrors, tc.minBeforeTotalErrs)
			}
			if result.After.Parse.Errors != tc.wantAfterParseErrs {
				t.Fatalf("after.parse.errors = %d, want %d", result.After.Parse.Errors, tc.wantAfterParseErrs)
			}
			if result.After.TotalErrors != tc.wantAfterTotalErrs {
				t.Fatalf("after.total_errors = %d, want %d", result.After.TotalErrors, tc.wantAfterTotalErrs)
			}

			gotKinds := make([]string, 0, len(result.Repair.Changes))
			for _, change := range result.Repair.Changes {
				gotKinds = append(gotKinds, change.Kind)
			}
			if !reflect.DeepEqual(gotKinds, tc.wantChangeKinds) {
				t.Fatalf("change kinds = %#v, want %#v", gotKinds, tc.wantChangeKinds)
			}
		})
	}
}

func readCorpusFixture(t *testing.T, path string) []byte {
	t.Helper()
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return src
}
