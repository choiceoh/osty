package llvmgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/check"
	ostyir "github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/mir"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

type lirProtoShadowFixture struct {
	Name       string
	SourcePath string
	Source     string
	Tags       []string
	Needles    []string
}

func TestLIRProtoCurrentGeneratorSourceFixturesShadowParity(t *testing.T) {
	fixtures := loadLIRProtoCurrentGeneratorSourceFixtures(t)
	if got, want := len(fixtures), 41; got != want {
		t.Fatalf("current-generator fixture count = %d, want %d", got, want)
	}

	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.Name, func(t *testing.T) {
			mirMod := lowerLIRProtoShadowSourceToMIR(t, fixture.Source)
			out, err := GenerateFromMIR(mirMod, Options{PackageName: "main", SourcePath: fixture.SourcePath})
			if err != nil {
				t.Fatalf("GenerateFromMIR: %v", err)
			}
			got := string(out)
			for _, want := range fixture.Needles {
				if !strings.Contains(got, want) {
					t.Fatalf("current MIR generator output missing %q for %s:\n%s", want, fixture.Name, got)
				}
			}
		})
	}
}

func loadLIRProtoCurrentGeneratorSourceFixtures(t *testing.T) []lirProtoShadowFixture {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	path := filepath.Join(root, "toolchain", "lir_proto_parity.osty")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	file, diags := parser.ParseDiagnostics(src)
	if len(diags) != 0 {
		t.Fatalf("parse %s diagnostics = %v", path, diags)
	}
	if file == nil {
		t.Fatalf("parse %s returned nil file", path)
	}

	sourceFixtures := map[string]lirProtoShadowFixture{}
	taggedCurrent := map[string]bool{}
	var currentHelper *ast.FnDecl
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FnDecl)
		if !ok {
			continue
		}
		if fn.Name == "lirParityCurrentGeneratorSourceFixtures" {
			currentHelper = fn
			continue
		}
		if !strings.HasPrefix(fn.Name, "lirParitySource") || !strings.HasSuffix(fn.Name, "Fixture") {
			continue
		}
		fixture, ok := parseLIRProtoSourceFixtureDecl(t, fn)
		if !ok {
			continue
		}
		sourceFixtures[fn.Name] = fixture
		if fixture.hasTag("current-generator") {
			taggedCurrent[fn.Name] = true
		}
	}
	if currentHelper == nil {
		t.Fatalf("%s does not define lirParityCurrentGeneratorSourceFixtures", path)
	}

	var fixtures []lirProtoShadowFixture
	for _, ref := range currentGeneratorSourceFixtureRefs(t, currentHelper) {
		fixture, ok := sourceFixtures[ref]
		if !ok {
			t.Fatalf("lirParityCurrentGeneratorSourceFixtures references unknown source fixture %s", ref)
		}
		if !taggedCurrent[ref] {
			t.Fatalf("%s is in lirParityCurrentGeneratorSourceFixtures without current-generator tag", ref)
		}
		if fixture.hasTag("lir-only") {
			t.Fatalf("%s is tagged as both current-generator and lir-only", fixture.Name)
		}
		fixtures = append(fixtures, fixture)
	}
	if len(fixtures) != len(taggedCurrent) {
		for name := range taggedCurrent {
			found := false
			for _, fixture := range fixtures {
				if sourceFixtures[name].Name == fixture.Name {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("%s has current-generator tag but is missing from lirParityCurrentGeneratorSourceFixtures", name)
			}
		}
		t.Fatalf("current-generator helper fixture count = %d, tagged fixture count = %d", len(fixtures), len(taggedCurrent))
	}
	return fixtures
}

func currentGeneratorSourceFixtureRefs(t *testing.T, fn *ast.FnDecl) []string {
	t.Helper()
	expr := blockFinalExpr(fn.Body)
	list, ok := expr.(*ast.ListExpr)
	if !ok {
		t.Fatalf("%s: expected fixture list, got %T", fn.Name, expr)
	}
	refs := make([]string, 0, len(list.Elems))
	for _, elem := range list.Elems {
		call, ok := elem.(*ast.CallExpr)
		if !ok || len(call.Args) != 0 {
			t.Fatalf("%s: expected zero-arg fixture call, got %T", fn.Name, elem)
		}
		ref := exprName(call.Fn)
		if ref == "" {
			t.Fatalf("%s: expected named fixture call, got %T", fn.Name, call.Fn)
		}
		refs = append(refs, ref)
	}
	return refs
}

func parseLIRProtoSourceFixtureDecl(t *testing.T, fn *ast.FnDecl) (lirProtoShadowFixture, bool) {
	t.Helper()
	if fn.Body == nil || len(fn.Body.Stmts) == 0 {
		return lirProtoShadowFixture{}, false
	}
	expr := blockFinalExpr(fn.Body)
	call, ok := expr.(*ast.CallExpr)
	if !ok || exprName(call.Fn) != "lirParityFixture" || len(call.Args) != 7 {
		return lirProtoShadowFixture{}, false
	}
	kind := exprName(call.Args[1].Value)
	if kind != "LirParitySource" {
		return lirProtoShadowFixture{}, false
	}
	name := stringArg(t, fn.Name, call.Args[0].Value)
	sourcePath := stringArg(t, fn.Name, call.Args[2].Value)
	source := stringArg(t, fn.Name, call.Args[3].Value)
	tags := stringListArg(t, fn.Name, call.Args[5].Value)
	needles := needleListArg(t, fn.Name, call.Args[6].Value)
	if name == "" || source == "" || len(needles) == 0 {
		t.Fatalf("%s has incomplete source fixture metadata", fn.Name)
	}
	return lirProtoShadowFixture{Name: name, SourcePath: sourcePath, Source: source, Tags: tags, Needles: needles}, true
}

func blockFinalExpr(block *ast.Block) ast.Expr {
	if block == nil || len(block.Stmts) == 0 {
		return nil
	}
	last := block.Stmts[len(block.Stmts)-1]
	switch stmt := last.(type) {
	case *ast.ExprStmt:
		return stmt.X
	case *ast.ReturnStmt:
		return stmt.Value
	default:
		return nil
	}
}

func exprName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.FieldExpr:
		base := exprName(e.X)
		if base == "" {
			return e.Name
		}
		return base + "." + e.Name
	default:
		return ""
	}
}

func stringArg(t *testing.T, owner string, expr ast.Expr) string {
	t.Helper()
	lit, ok := expr.(*ast.StringLit)
	if !ok || len(lit.Parts) != 1 || !lit.Parts[0].IsLit {
		t.Fatalf("%s: expected plain string literal, got %T", owner, expr)
	}
	return lit.Parts[0].Lit
}

func stringListArg(t *testing.T, owner string, expr ast.Expr) []string {
	t.Helper()
	list, ok := expr.(*ast.ListExpr)
	if !ok {
		t.Fatalf("%s: expected string list, got %T", owner, expr)
	}
	out := make([]string, 0, len(list.Elems))
	for _, elem := range list.Elems {
		out = append(out, stringArg(t, owner, elem))
	}
	return out
}

func needleListArg(t *testing.T, owner string, expr ast.Expr) []string {
	t.Helper()
	list, ok := expr.(*ast.ListExpr)
	if !ok {
		t.Fatalf("%s: expected needle list, got %T", owner, expr)
	}
	out := make([]string, 0, len(list.Elems))
	for _, elem := range list.Elems {
		call, ok := elem.(*ast.CallExpr)
		if !ok || exprName(call.Fn) != "lirParityNeedle" || len(call.Args) < 1 {
			t.Fatalf("%s: expected lirParityNeedle call, got %T", owner, elem)
		}
		out = append(out, stringArg(t, owner, call.Args[0].Value))
	}
	return out
}

func (f lirProtoShadowFixture) hasTag(tag string) bool {
	for _, existing := range f.Tags {
		if existing == tag {
			return true
		}
	}
	return false
}

// TestLIRProtoManualFixtureCatalog pins the Phase 4 runtime ABI fixture
// catalog (String/Bytes from 4a, List/Map/Set/Concurrency from 4b). It
// does not execute the Osty-side `lirLowerMirModule` — production wiring
// is gated on Phase 7 — but it does load the parity catalog and assert
// each fixture is present with the expected runtime symbol needles, so a
// silent catalog truncation or a runtime-symbol typo surfaces as a Go
// test failure even before the production switch lands.
func TestLIRProtoManualFixtureCatalog(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	path := filepath.Join(root, "toolchain", "lir_proto_parity.osty")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	file, diags := parser.ParseDiagnostics(src)
	if len(diags) != 0 {
		t.Fatalf("parse %s diagnostics = %v", path, diags)
	}

	manualFixtures := map[string]lirProtoShadowFixture{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FnDecl)
		if !ok {
			continue
		}
		if !strings.HasPrefix(fn.Name, "lirParityManual") || !strings.HasSuffix(fn.Name, "Fixture") {
			continue
		}
		fixture, ok := parseLIRProtoManualFixtureDecl(t, fn)
		if !ok {
			continue
		}
		manualFixtures[fn.Name] = fixture
	}

	want := []struct {
		name    string
		needles []string
	}{
		{"lirParityManualStringByteLenFixture", []string{"declare i64 @osty_rt_strings_ByteLen(ptr)", "call i64 @osty_rt_strings_ByteLen("}},
		{"lirParityManualStringIsEmptyFixture", []string{"declare i64 @osty_rt_strings_ByteLen(ptr)", "icmp eq i64"}},
		{"lirParityManualStringConcatBinaryFixture", []string{"declare ptr @osty_rt_strings_Concat(ptr, ptr)", "call ptr @osty_rt_strings_Concat("}},
		{"lirParityManualStringConcatNFixture", []string{"declare ptr @osty_rt_strings_ConcatN(i64, ptr)", "alloca [3 x ptr]", "call ptr @osty_rt_strings_ConcatN(i64 3,"}},
		{"lirParityManualStringContainsFixture", []string{"declare i1 @osty_rt_strings_Contains(ptr, ptr)", "call i1 @osty_rt_strings_Contains("}},
		{"lirParityManualStringTrimSpaceFixture", []string{"declare ptr @osty_rt_strings_TrimSpace(ptr)", "call ptr @osty_rt_strings_TrimSpace("}},
		{"lirParityManualStringRepeatFixture", []string{"declare ptr @osty_rt_strings_Repeat(ptr, i64)", "call ptr @osty_rt_strings_Repeat("}},
		{"lirParityManualStringSubstringFixture", []string{"declare ptr @osty_rt_strings_Slice(ptr, i64, i64)", "call ptr @osty_rt_strings_Slice("}},
		{"lirParityManualStringReplaceAllFixture", []string{"declare ptr @osty_rt_strings_ReplaceAll(ptr, ptr, ptr)", "call ptr @osty_rt_strings_ReplaceAll("}},
		{"lirParityManualBytesLenFixture", []string{"declare i64 @osty_rt_bytes_len(ptr)", "call i64 @osty_rt_bytes_len("}},
		{"lirParityManualBytesConcatFixture", []string{"declare ptr @osty_rt_bytes_concat(ptr, ptr)", "call ptr @osty_rt_bytes_concat("}},
		{"lirParityManualBytesSliceFixture", []string{"declare ptr @osty_rt_bytes_slice(ptr, i64, i64)", "call ptr @osty_rt_bytes_slice("}},
		// ----- Phase 4b: List / Map / Set / Concurrency runtime ABI -----
		{"lirParityManualListPushIntFixture", []string{"declare void @osty_rt_list_push_i64(ptr, i64)", "call void @osty_rt_list_push_i64("}},
		{"lirParityManualListPushStringFixture", []string{"declare void @osty_rt_list_push_string(ptr, ptr)", "call void @osty_rt_list_push_string("}},
		{"lirParityManualListLenFixture", []string{"declare i64 @osty_rt_list_len(ptr)", "call i64 @osty_rt_list_len("}},
		{"lirParityManualListIsEmptyFixture", []string{"declare i64 @osty_rt_list_len(ptr)", "icmp eq i64"}},
		{"lirParityManualListGetFloatFixture", []string{"declare double @osty_rt_list_get_f64(ptr, i64)", "call double @osty_rt_list_get_f64("}},
		{"lirParityManualListSortedI64Fixture", []string{"declare ptr @osty_rt_list_sorted_i64(ptr)", "call ptr @osty_rt_list_sorted_i64("}},
		{"lirParityManualListReversedFixture", []string{"declare ptr @osty_rt_list_reversed(ptr)", "call ptr @osty_rt_list_reversed("}},
		{"lirParityManualListClearFixture", []string{"declare void @osty_rt_list_clear(ptr)", "call void @osty_rt_list_clear("}},
		{"lirParityManualMapNewFixture", []string{"declare ptr @osty_rt_map_new()", "call ptr @osty_rt_map_new()"}},
		{"lirParityManualMapInsertStringIntFixture", []string{"declare void @osty_rt_map_insert_string(ptr, ptr, ptr)", "alloca i64", "call void @osty_rt_map_insert_string("}},
		{"lirParityManualMapContainsI64Fixture", []string{"declare i1 @osty_rt_map_contains_i64(ptr, i64)", "call i1 @osty_rt_map_contains_i64("}},
		{"lirParityManualMapKeysFixture", []string{"declare ptr @osty_rt_map_keys(ptr)", "call ptr @osty_rt_map_keys("}},
		{"lirParityManualMapLenFixture", []string{"declare i64 @osty_rt_map_len(ptr)", "call i64 @osty_rt_map_len("}},
		{"lirParityManualSetInsertStringFixture", []string{"declare i1 @osty_rt_set_insert_string(ptr, ptr)", "call i1 @osty_rt_set_insert_string("}},
		{"lirParityManualSetContainsI64Fixture", []string{"declare i1 @osty_rt_set_contains_i64(ptr, i64)", "call i1 @osty_rt_set_contains_i64("}},
		{"lirParityManualSetToListFixture", []string{"declare ptr @osty_rt_set_to_list(ptr)", "call ptr @osty_rt_set_to_list("}},
		{"lirParityManualChanCloseFixture", []string{"declare void @osty_rt_thread_chan_close(ptr)", "call void @osty_rt_thread_chan_close("}},
		{"lirParityManualYieldFixture", []string{"declare void @osty_rt_task_yield()", "call void @osty_rt_task_yield()"}},
		{"lirParityManualIsCancelledFixture", []string{"declare i1 @osty_rt_cancel_is_cancelled()", "call i1 @osty_rt_cancel_is_cancelled()"}},
		// ----- Phase 5: GC entry safepoint + function/parameter attributes -----
		{"lirParityManualEntrySafepointFixture", []string{"declare void @osty.gc.safepoint_v1(i64, ptr, i64)", "call void @osty.gc.safepoint_v1(i64 72057594037927936, ptr null, i64 0)"}},
		{"lirParityManualFnAttrInlineAlwaysFixture", []string{"alwaysinline"}},
		{"lirParityManualFnAttrHotPureFixture", []string{" hot ", " readnone "}},
		{"lirParityManualFnAttrTargetFeaturesFixture", []string{"\"target-features\"=\"+avx512f,+avx512bw\""}},
		{"lirParityManualFnAttrNoaliasFixture", []string{"ptr noalias %p1", "ptr noalias %p2"}},
		// ----- Phase 4 deferred slice: IndexOf-family Option<Int> wrapping -----
		{"lirParityManualStringIndexOfRawFixture", []string{"declare i64 @osty_rt_strings_IndexOf(ptr, ptr)", "call i64 @osty_rt_strings_IndexOf(", "ret i64"}},
		{"lirParityManualStringIndexOfOptionFixture", []string{"%Option.Int = type", "declare i64 @osty_rt_strings_IndexOf(ptr, ptr)", "icmp sge i64", "alloca %Option.Int", "insertvalue %Option.Int undef, i64 1, 0", "insertvalue %Option.Int undef, i64 0, 0", "load %Option.Int", "ret %Option.Int"}},
		{"lirParityManualBytesIndexOfOptionFixture", []string{"%Option.Int = type", "declare i64 @osty_rt_bytes_index_of(ptr, ptr)", "icmp sge i64", "ret %Option.Int"}},
		// ----- Slice B: Channel send/recv per-element ABI -----
		{"lirParityManualChanMakeFixture", []string{"declare ptr @osty_rt_thread_chan_make(i64)", "call ptr @osty_rt_thread_chan_make("}},
		{"lirParityManualChanIsClosedFixture", []string{"declare i1 @osty_rt_thread_chan_is_closed(ptr)", "call i1 @osty_rt_thread_chan_is_closed("}},
		{"lirParityManualChanSendIntFixture", []string{"declare void @osty_rt_thread_chan_send_i64(ptr, i64)", "call void @osty_rt_thread_chan_send_i64("}},
		{"lirParityManualChanRecvIntFixture", []string{"%Option.Int = type", "declare %Option.Int @osty_rt_thread_chan_recv_i64(ptr)", "call %Option.Int @osty_rt_thread_chan_recv_i64(", "ret %Option.Int"}},
		// ----- String parse → Result<T, Error> -----
		{"lirParityManualStringToIntFixture", []string{"%Result.Int_Error = type", "declare i1 @osty_rt_strings_IsValidInt(ptr)", "declare i64 @osty_rt_strings_ToInt(ptr)", "alloca %Result.Int_Error", "insertvalue %Result.Int_Error undef, i64 1, 0", "insertvalue %Result.Int_Error undef, i64 0, 0", "load %Result.Int_Error", "ret %Result.Int_Error"}},
		{"lirParityManualStringToFloatFixture", []string{"%Result.Float_Error = type", "declare i1 @osty_rt_strings_IsValidFloat(ptr)", "declare double @osty_rt_strings_ToFloat(ptr)", "bitcast double", "ret %Result.Float_Error"}},
		// ----- Map.get → Option<V> -----
		{"lirParityManualMapGetStringIntFixture", []string{"%Option.Int = type", "declare i1 @osty_rt_map_get_string(ptr, ptr, ptr)", "alloca i64", "call i1 @osty_rt_map_get_string(", "alloca %Option.Int", "load i64", "insertvalue %Option.Int undef, i64 1, 0", "insertvalue %Option.Int undef, i64 0, 0", "ret %Option.Int"}},
		{"lirParityManualMapGetI64StringFixture", []string{"%Option.String = type", "declare i1 @osty_rt_map_get_i64(ptr, i64, ptr)", "alloca ptr", "call i1 @osty_rt_map_get_i64(", "ptrtoint ptr", "ret %Option.String"}},
		// ----- List.first / List.last → Option<T> -----
		{"lirParityManualListFirstIntFixture", []string{"%Option.Int = type", "declare i64 @osty_rt_list_len(ptr)", "declare i64 @osty_rt_list_get_i64(ptr, i64)", "icmp eq i64", "alloca %Option.Int", "call i64 @osty_rt_list_get_i64(", "insertvalue %Option.Int undef, i64 1, 0", "insertvalue %Option.Int undef, i64 0, 0", "ret %Option.Int"}},
		{"lirParityManualListLastFloatFixture", []string{"%Option.Float = type", "declare i64 @osty_rt_list_len(ptr)", "declare double @osty_rt_list_get_f64(ptr, i64)", "sub i64", "call double @osty_rt_list_get_f64(", "bitcast double", "ret %Option.Float"}},
		// ----- Batched: ListPop / BytesGet / ToString / CheckCancelled -----
		{"lirParityManualListPopIntFixture", []string{"%Option.Int = type", "declare void @osty_rt_list_pop_discard(ptr)", "declare i64 @osty_rt_list_get_i64(ptr, i64)", "icmp eq i64", "sub i64", "call i64 @osty_rt_list_get_i64(", "call void @osty_rt_list_pop_discard(", "ret %Option.Int"}},
		{"lirParityManualBytesGetFixture", []string{"%Option.Byte = type", "declare i64 @osty_rt_bytes_len(ptr)", "declare i8 @osty_rt_bytes_get(ptr, i64)", "icmp sge i64", "icmp slt i64", "and i1", "call i8 @osty_rt_bytes_get(", "zext i8", "ret %Option.Byte"}},
		{"lirParityManualListToStringIntFixture", []string{"declare ptr @osty_rt_list_to_string_i64(ptr)", "call ptr @osty_rt_list_to_string_i64("}},
		{"lirParityManualMapToStringFixture", []string{"declare ptr @osty_rt_map_to_string(ptr)", "call ptr @osty_rt_map_to_string("}},
		{"lirParityManualSetToStringFixture", []string{"declare ptr @osty_rt_set_to_string(ptr)", "call ptr @osty_rt_set_to_string("}},
		{"lirParityManualCheckCancelledFixture", []string{"declare { i64, i64 } @osty_rt_cancel_check_cancelled()", "call { i64, i64 } @osty_rt_cancel_check_cancelled()"}},
		// ----- Batched: bytes-v1 / concurrency / Map fused / Bytes conversions -----
		{"lirParityManualListPushBytesV1Fixture", []string{"%Cell = type", "declare void @osty_rt_list_push_bytes_v1(ptr, ptr, i64)", "alloca %Cell", "getelementptr inbounds %Cell, ptr null, i32 1", "ptrtoint ptr", "call void @osty_rt_list_push_bytes_v1("}},
		{"lirParityManualHandleJoinIntFixture", []string{"declare i64 @osty_rt_task_handle_join(ptr)", "call i64 @osty_rt_task_handle_join("}},
		{"lirParityManualGroupCancelFixture", []string{"declare void @osty_rt_task_group_cancel(ptr)", "call void @osty_rt_task_group_cancel("}},
		{"lirParityManualGroupIsCancelledFixture", []string{"declare i1 @osty_rt_task_group_is_cancelled(ptr)", "call i1 @osty_rt_task_group_is_cancelled("}},
		{"lirParityManualSleepFixture", []string{"declare void @osty_rt_thread_sleep(ptr)", "call void @osty_rt_thread_sleep("}},
		{"lirParityManualMapKeysSortedI64Fixture", []string{"declare ptr @osty_rt_map_keys_sorted_i64(ptr)", "call ptr @osty_rt_map_keys_sorted_i64("}},
		{"lirParityManualBytesFromStringFixture", []string{"declare ptr @osty_rt_strings_ToBytes(ptr)", "call ptr @osty_rt_strings_ToBytes("}},
		{"lirParityManualBytesFromListFixture", []string{"declare ptr @osty_rt_bytes_from_list(ptr)", "call ptr @osty_rt_bytes_from_list("}},
		{"lirParityManualBytesToStringFixture", []string{"%Result.String_Error = type", "declare i1 @osty_rt_bytes_is_valid_utf8(ptr)", "declare ptr @osty_rt_bytes_to_string(ptr)", "ptrtoint ptr", "ret %Result.String_Error"}},
		{"lirParityManualBytesFromHexFixture", []string{"%Result.Bytes_Error = type", "declare i1 @osty_rt_bytes_is_valid_hex(ptr)", "declare ptr @osty_rt_bytes_from_hex(ptr)", "ret %Result.Bytes_Error"}},
		// ----- Batched: bytes-v1 expand + Set i1 return type pin -----
		{"lirParityManualListGetBytesV1Fixture", []string{"%Cell = type", "declare void @osty_rt_list_get_bytes_v1(ptr, i64, ptr, i64)", "alloca %Cell", "getelementptr inbounds %Cell, ptr null, i32 1", "call void @osty_rt_list_get_bytes_v1(", "load %Cell"}},
		{"lirParityManualListInsertBytesV1Fixture", []string{"%Cell = type", "declare void @osty_rt_list_insert_bytes_v1(ptr, i64, ptr, i64)", "alloca %Cell", "call void @osty_rt_list_insert_bytes_v1("}},
		{"lirParityManualChanSendBytesV1Fixture", []string{"%Cell = type", "declare void @osty_rt_thread_chan_send_bytes_v1(ptr, ptr, i64)", "alloca %Cell", "call void @osty_rt_thread_chan_send_bytes_v1("}},
		{"lirParityManualSetRemoveI1Fixture", []string{"declare i1 @osty_rt_set_remove_i64(ptr, i64)", "call i1 @osty_rt_set_remove_i64("}},
		// ----- Concurrency closure-env intrinsics -----
		{"lirParityManualTaskGroupUnitFixture", []string{"declare void @osty_rt_task_group_root(ptr)", "call void @osty_rt_task_group_root("}},
		{"lirParityManualSpawnDetachedFixture", []string{"declare ptr @osty_rt_task_spawn(ptr)", "call ptr @osty_rt_task_spawn("}},
		{"lirParityManualSpawnGroupedFixture", []string{"declare ptr @osty_rt_task_group_spawn(ptr, ptr)", "call ptr @osty_rt_task_group_spawn("}},
		{"lirParityManualSelectFixture", []string{"declare void @osty_rt_select(ptr)", "call void @osty_rt_select("}},
		{"lirParityManualSelectRecvFixture", []string{"declare void @osty_rt_select_recv(ptr, ptr, ptr)", "call void @osty_rt_select_recv("}},
		{"lirParityManualSelectSendIntFixture", []string{"declare void @osty_rt_select_send_i64(ptr, ptr, i64, ptr)", "call void @osty_rt_select_send_i64("}},
		{"lirParityManualSelectTimeoutFixture", []string{"declare void @osty_rt_select_timeout(ptr, ptr, ptr)", "call void @osty_rt_select_timeout("}},
		{"lirParityManualSelectDefaultFixture", []string{"declare void @osty_rt_select_default(ptr, ptr)", "call void @osty_rt_select_default("}},
		{"lirParityManualParallelFixture", []string{"declare ptr @osty_rt_parallel(ptr, i64, ptr)", "call ptr @osty_rt_parallel("}},
		{"lirParityManualRaceFixture", []string{"%Result.Int_Error = type", "declare { i64, i64 } @osty_rt_task_race(ptr)", "call { i64, i64 } @osty_rt_task_race("}},
		{"lirParityManualCollectAllFixture", []string{"declare ptr @osty_rt_task_collect_all(ptr)", "call ptr @osty_rt_task_collect_all("}},
		// ----- GC root binding -----
		{"lirParityManualGcRootBindReleaseFixture", []string{"declare void @osty.gc.root_bind_v1(ptr)", "declare void @osty.gc.root_release_v1(ptr)", "call void @osty.gc.root_bind_v1(", "call void @osty.gc.root_release_v1("}},
		{"lirParityManualGcRootMultipleSlotsFixture", []string{"call void @osty.gc.root_bind_v1(ptr %l1)", "call void @osty.gc.root_bind_v1(ptr %l2)", "call void @osty.gc.root_release_v1(ptr %l2)", "call void @osty.gc.root_release_v1(ptr %l1)"}},
		{"lirParityManualGcRootScalarOnlyFixture", []string{"define i64 @scalarOnly(i64", "ret i64"}},
		// ----- Loop metadata (back-edge → !llvm.loop) -----
		{"lirParityManualLoopMDVectorizeFixture", []string{"!{!\"llvm.loop.vectorize.enable\", i1 true}", "= distinct !{", ", !llvm.loop !"}},
		{"lirParityManualLoopMDUnrollCountFixture", []string{"!{!\"llvm.loop.unroll.count\", i32 4}", "= distinct !{", ", !llvm.loop !"}},
		{"lirParityManualLoopMDPlainNoMDFixture", []string{"define void @plainLoop()", "br label %bb0"}},
		// ----- Per-instruction !llvm.access.group metadata (#[parallel]) -----
		{"lirParityManualParallelAccessGroupFixture", []string{"= distinct !{}", "store i64", ", !llvm.access.group !", " = load i64"}},
		{"lirParityManualParallelLoopWithAccessGroupFixture", []string{"= distinct !{}", "!{!\"llvm.loop.parallel_accesses\", !", ", !llvm.loop !", ", !llvm.access.group !"}},
		// ----- Loop md tuning property fixtures -----
		{"lirParityManualLoopMDVectorizeWidthFixture", []string{"!{!\"llvm.loop.vectorize.enable\", i1 true}", "!{!\"llvm.loop.vectorize.width\", i32 4}", ", !llvm.loop !"}},
		{"lirParityManualLoopMDVectorizeFullFixture", []string{"!{!\"llvm.loop.vectorize.enable\", i1 true}", "!{!\"llvm.loop.vectorize.width\", i32 8}", "!{!\"llvm.loop.vectorize.scalable.enable\", i1 true}", "!{!\"llvm.loop.vectorize.predicate.enable\", i1 true}"}},
		{"lirParityManualLoopMDUnrollEnableBareFixture", []string{"!{!\"llvm.loop.unroll.enable\", i1 true}", ", !llvm.loop !"}},
		{"lirParityManualLoopMDCombinedVectorizeUnrollFixture", []string{"!{!\"llvm.loop.vectorize.enable\", i1 true}", "!{!\"llvm.loop.unroll.count\", i32 2}", ", !llvm.loop !"}},
		{"lirParityManualLoopMDParallelOnlyFixture", []string{"= distinct !{}", "!{!\"llvm.loop.parallel_accesses\", !", ", !llvm.loop !"}},
		// ----- Option / Result inspection intrinsics + RawNull -----
		{"lirParityManualOptionIsSomeFixture", []string{"%Option.Int = type", "extractvalue %Option.Int", "icmp ne i64", "ret i1"}},
		{"lirParityManualOptionIsNoneFixture", []string{"%Option.Int = type", "extractvalue %Option.Int", "icmp eq i64", "ret i1"}},
		{"lirParityManualResultIsOkFixture", []string{"%Result.Int_Error = type", "extractvalue %Result.Int_Error", "icmp ne i64", "ret i1"}},
		{"lirParityManualResultIsErrFixture", []string{"%Result.Int_Error = type", "extractvalue %Result.Int_Error", "icmp eq i64", "ret i1"}},
		{"lirParityManualRawNullFixture", []string{"define ptr @rawNull()", "store ptr null", "ret ptr"}},
		// ----- Element-agnostic List runtime + per-lane List.toSet + String split/fields -----
		{"lirParityManualListSliceFixture", []string{"declare ptr @osty_rt_list_slice(ptr, i64, i64)", "call ptr @osty_rt_list_slice(", "ret ptr"}},
		{"lirParityManualListToSetI64Fixture", []string{"declare ptr @osty_rt_list_to_set_i64(ptr)", "call ptr @osty_rt_list_to_set_i64(", "ret ptr"}},
		{"lirParityManualListToSetStringFixture", []string{"declare ptr @osty_rt_list_to_set_string(ptr)", "call ptr @osty_rt_list_to_set_string(", "ret ptr"}},
		{"lirParityManualStringFieldsFixture", []string{"declare ptr @osty_rt_strings_Fields(ptr)", "call ptr @osty_rt_strings_Fields(", "ret ptr"}},
		{"lirParityManualStringSplitNFixture", []string{"declare ptr @osty_rt_strings_SplitN(ptr, ptr, i64)", "call ptr @osty_rt_strings_SplitN(", "ret ptr"}},
		// ----- Option/Result Unwrap + UnwrapOr -----
		{"lirParityManualOptionUnwrapFixture", []string{"%Option.Int = type", "declare void @osty_rt_option_unwrap_none()", "extractvalue %Option.Int", "icmp eq i64", "call void @osty_rt_option_unwrap_none()", "unreachable", "ret i64"}},
		{"lirParityManualOptionUnwrapOrFixture", []string{"%Option.Int = type", "extractvalue %Option.Int", "icmp eq i64", "alloca i64", "ret i64"}},
		{"lirParityManualResultUnwrapFixture", []string{"%Result.Int_Error = type", "declare void @osty_rt_result_unwrap_err()", "extractvalue %Result.Int_Error", "icmp eq i64", "call void @osty_rt_result_unwrap_err()", "unreachable", "ret i64"}},
		{"lirParityManualResultUnwrapOrFixture", []string{"%Result.Int_Error = type", "extractvalue %Result.Int_Error", "icmp eq i64", "alloca i64", "ret i64"}},
		// ----- ListRemoveAt + MapGetOr + StringSplitInto + StringNthSegment -----
		{"lirParityManualListRemoveAtFixture", []string{"declare i64 @osty_rt_list_get_i64(ptr, i64)", "declare void @osty_rt_list_remove_at_discard(ptr, i64)", "call i64 @osty_rt_list_get_i64(", "call void @osty_rt_list_remove_at_discard(", "ret i64"}},
		{"lirParityManualMapGetOrFixture", []string{"declare i1 @osty_rt_map_get_string(ptr, ptr, ptr)", "alloca i64", "call i1 @osty_rt_map_get_string(", "ret i64"}},
		{"lirParityManualStringSplitIntoFixture", []string{"declare void @osty_rt_strings_SplitInto(ptr, ptr, ptr)", "call void @osty_rt_strings_SplitInto("}},
		{"lirParityManualStringNthSegmentFixture", []string{"declare ptr @osty_rt_strings_NthSegment(ptr, ptr, i64)", "call ptr @osty_rt_strings_NthSegment(", "ret ptr"}},
		// ----- MapIncr + ListContains + ListIndexOf -----
		{"lirParityManualMapIncrStringFixture", []string{"declare i64 @osty_rt_map_incr_i64_string(ptr, ptr, i64)", "call i64 @osty_rt_map_incr_i64_string(", "ret i64"}},
		{"lirParityManualListContainsI64Fixture", []string{"declare i64 @osty_rt_list_len(ptr)", "declare i64 @osty_rt_list_get_i64(ptr, i64)", "alloca i1", "alloca i64", "icmp slt i64", "icmp eq i64", "ret i1"}},
		{"lirParityManualListIndexOfStringFixture", []string{"%Option.Int = type", "declare ptr @osty_rt_list_get_string(ptr, i64)", "declare i1 @osty_rt_strings_Equal(ptr, ptr)", "alloca %Option.Int", "call i1 @osty_rt_strings_Equal(", "ret %Option.Int"}},
		// ----- lirNarrowI64ToType per-branch coverage -----
		{"lirParityManualOptionUnwrapStringFixture", []string{"%Option.String = type", "declare void @osty_rt_option_unwrap_none()", "extractvalue %Option.String", "inttoptr i64", "ret ptr"}},
		{"lirParityManualOptionUnwrapFloatFixture", []string{"%Option.Float = type", "declare void @osty_rt_option_unwrap_none()", "extractvalue %Option.Float", "bitcast i64", "ret double"}},
		{"lirParityManualOptionUnwrapBoolFixture", []string{"%Option.Bool = type", "declare void @osty_rt_option_unwrap_none()", "extractvalue %Option.Bool", "trunc i64", "ret i1"}},
		{"lirParityManualOptionUnwrapOrStringFixture", []string{"%Option.String = type", "extractvalue %Option.String", "alloca ptr", "inttoptr i64", "ret ptr"}},
		// ----- MirRVDiscriminant + MirRVLen rvalue dispatch -----
		{"lirParityManualDiscriminantExtractIntFixture", []string{"%Option.Int = type", "define i64 @whichTag(", "extractvalue %Option.Int", ", 0", "ret i64"}},
		{"lirParityManualLenRValueListIntFixture", []string{"declare i64 @osty_rt_list_len(ptr)", "define i64 @lenViaRV(ptr", "call i64 @osty_rt_list_len(", "ret i64"}},
		// ----- MirRVNullary + MirCastOptionalWrap/Unwrap -----
		{"lirParityManualNullaryNoneOptionIntFixture", []string{"%Option.Int = type", "define %Option.Int @noneRV()", "insertvalue %Option.Int undef, i64 0, 0", "insertvalue %Option.Int", "ret %Option.Int"}},
		{"lirParityManualCastOptionalWrapPassthroughFixture", []string{"define i64 @wrapId(i64", "ret i64"}},
		// ----- MirAggEnumVariant aggregate -----
		{"lirParityManualAggEnumVariantSomeIntFixture", []string{"%Option.Int = type", "define %Option.Int @wrapSome(i64", "insertvalue %Option.Int undef, i64 1, 0", "insertvalue %Option.Int", "ret %Option.Int"}},
		// ----- MirAggList aggregate -----
		{"lirParityManualAggListIntLiteralFixture", []string{"declare ptr @osty_rt_list_new()", "declare void @osty_rt_list_push_i64(ptr, i64)", "call ptr @osty_rt_list_new()", "call void @osty_rt_list_push_i64(", "ret ptr"}},
		{"lirParityManualAggListStructBytesV1Fixture", []string{"%Pair = type { i64, i64 }", "declare ptr @osty_rt_list_new()", "declare void @osty_rt_list_push_bytes_v1(ptr, ptr, i64)", "call ptr @osty_rt_list_new()", "alloca %Pair", "store %Pair", "getelementptr inbounds %Pair, ptr null, i32 1", "ptrtoint ptr", "call void @osty_rt_list_push_bytes_v1(", "ret ptr"}},
		{"lirParityManualListRemoveAtStructBytesV1Fixture", []string{"%Pair = type { i64, i64 }", "declare void @osty_rt_list_get_bytes_v1(ptr, i64, ptr, i64)", "declare void @osty_rt_list_remove_at_discard(ptr, i64)", "alloca %Pair", "getelementptr inbounds %Pair, ptr null, i32 1", "ptrtoint ptr", "call void @osty_rt_list_get_bytes_v1(", "load %Pair", "call void @osty_rt_list_remove_at_discard(", "ret %Pair"}},
	}

	for _, tt := range want {
		fixture, ok := manualFixtures[tt.name]
		if !ok {
			t.Fatalf("Phase 4 fixture %s missing from parity catalog", tt.name)
		}
		joined := strings.Join(fixture.Needles, "|")
		for _, needle := range tt.needles {
			if !strings.Contains(joined, needle) {
				t.Fatalf("%s missing needle %q (have: %v)", tt.name, needle, fixture.Needles)
			}
		}
	}
}

func parseLIRProtoManualFixtureDecl(t *testing.T, fn *ast.FnDecl) (lirProtoShadowFixture, bool) {
	t.Helper()
	if fn.Body == nil || len(fn.Body.Stmts) == 0 {
		return lirProtoShadowFixture{}, false
	}
	expr := blockFinalExpr(fn.Body)
	call, ok := expr.(*ast.CallExpr)
	if !ok || exprName(call.Fn) != "lirParityFixture" || len(call.Args) != 7 {
		return lirProtoShadowFixture{}, false
	}
	if exprName(call.Args[1].Value) != "LirParityManualMIR" {
		return lirProtoShadowFixture{}, false
	}
	name := stringArg(t, fn.Name, call.Args[0].Value)
	sourcePath := stringArg(t, fn.Name, call.Args[2].Value)
	tags := stringListArg(t, fn.Name, call.Args[5].Value)
	needles := needleListArg(t, fn.Name, call.Args[6].Value)
	if name == "" || len(needles) == 0 {
		return lirProtoShadowFixture{}, false
	}
	return lirProtoShadowFixture{Name: name, SourcePath: sourcePath, Tags: tags, Needles: needles}, true
}

// TestLIRProtoFixtureCatalogShape (Phase 6) is a single Go-side pin
// that walks every parity fixture in toolchain/lir_proto_parity.osty
// and asserts the catalog-wide invariants the Osty self-test only
// checks indirectly: every fixture has a non-empty name, source path,
// and at least one needle, every source fixture is tagged with either
// `current-generator` or `lir-only` so the shadow parity loader can
// route it, and no fixture name appears twice. Acts as an early-warning
// for catalog drift before either the manual-MIR or source-fixture
// runners would surface the problem at slice-add time.
func TestLIRProtoFixtureCatalogShape(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	path := filepath.Join(root, "toolchain", "lir_proto_parity.osty")
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	file, diags := parser.ParseDiagnostics(src)
	if len(diags) != 0 {
		t.Fatalf("parse %s diagnostics = %v", path, diags)
	}

	manualCount := 0
	sourceCount := 0
	seen := map[string]string{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FnDecl)
		if !ok {
			continue
		}
		var fixture lirProtoShadowFixture
		var ok2 bool
		if strings.HasPrefix(fn.Name, "lirParityManual") && strings.HasSuffix(fn.Name, "Fixture") {
			fixture, ok2 = parseLIRProtoManualFixtureDecl(t, fn)
			if ok2 {
				manualCount++
			}
		}
		if strings.HasPrefix(fn.Name, "lirParitySource") && strings.HasSuffix(fn.Name, "Fixture") {
			fixture, ok2 = parseLIRProtoSourceFixtureDecl(t, fn)
			if ok2 {
				sourceCount++
				if !fixture.hasTag("current-generator") && !fixture.hasTag("lir-only") {
					t.Fatalf("source fixture %s must carry either `current-generator` or `lir-only` tag (have %v)", fn.Name, fixture.Tags)
				}
			}
		}
		if !ok2 {
			continue
		}
		if fixture.Name == "" {
			t.Fatalf("%s: fixture name is empty", fn.Name)
		}
		if fixture.SourcePath == "" {
			t.Fatalf("%s: fixture source path is empty (catalog requires a stable anchor for diagnostics)", fn.Name)
		}
		if len(fixture.Needles) == 0 {
			t.Fatalf("%s: fixture has zero needles — must pin at least one rendered shape", fn.Name)
		}
		if owner, dup := seen[fixture.Name]; dup {
			t.Fatalf("duplicate fixture name %q: defined in both %s and %s", fixture.Name, owner, fn.Name)
		}
		seen[fixture.Name] = fn.Name
	}
	if manualCount < 16 {
		t.Fatalf("manual fixture count = %d, want >= 16 (Phase-3 baseline)", manualCount)
	}
	if sourceCount < 9 {
		t.Fatalf("source fixture count = %d, want >= 9 (Phase-3 source-parity baseline)", sourceCount)
	}
}

func lowerLIRProtoShadowSourceToMIR(t *testing.T, src string) *mir.Module {
	t.Helper()
	source := []byte(src)
	file, parseDiags := parser.ParseDiagnostics(source)
	if len(parseDiags) != 0 {
		t.Fatalf("parse diagnostics = %v", parseDiags)
	}
	if file == nil {
		t.Fatal("parser returned nil file")
	}
	reg := stdlib.LoadCached()
	res := resolve.ResolveFileSourceDefault(source, file, reg)
	if len(res.Diags) != 0 {
		t.Fatalf("resolve diagnostics = %v", res.Diags)
	}
	chk := check.SelfhostFile(file, res, check.Opts{
		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        source,
	})
	if len(chk.Diags) != 0 {
		t.Fatalf("check diagnostics = %v", chk.Diags)
	}
	hirMod, issues := ostyir.Lower("main", file, res, chk)
	if len(issues) != 0 {
		t.Fatalf("ir.Lower issues = %v", issues)
	}
	monoMod, monoErrs := ostyir.Monomorphize(hirMod)
	if len(monoErrs) != 0 {
		t.Fatalf("ir.Monomorphize errors = %v", monoErrs)
	}
	if errs := ostyir.Validate(monoMod); len(errs) != 0 {
		t.Fatalf("ir.Validate errors = %v", errs)
	}
	mirMod := mir.Lower(monoMod)
	if mirMod == nil {
		t.Fatal("mir.Lower returned nil")
	}
	if errs := mir.Validate(mirMod); len(errs) != 0 {
		t.Fatalf("mir.Validate errors = %v", errs)
	}
	return mirMod
}
