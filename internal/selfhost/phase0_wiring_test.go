package selfhost

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestPhase0SelfHostWiringExists guards the Phase 0 self-host backend
// wiring: the chain `hirLowerSource` → `mirLowerModule` (which itself
// calls `hirMonomorphizeModule`) → `mirEmitModule` must be present in
// the toolchain source, and `toolchain/main.osty` must dispatch the
// `compile <file>` subcommand to that chain.
//
// The wiring is structural-only today because `osty-self` itself
// can't yet be built via the LLVM backend (the backend trips on
// stdlib body walls before reaching the new entry points — see
// SELFHOST_PORT_MATRIX.md). When the backend catches up this test
// gains a sibling that actually invokes `osty-self compile` on a
// trivial source and asserts the IR contains `target triple` plus
// `define void @main`. Until then the structural gate is the
// authoritative regression guard: any future PR that drops or
// renames a Phase 0 entry point fails this test before the next
// session's runtime test would.
//
// Phase 0 = module-skeleton emission (header + define-headers +
// `entry:` stub returns). Phase 1+ replaces the stub returns with
// real per-instruction lowering.
func TestPhase0SelfHostWiringExists(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}

	cases := []struct {
		path    string
		needles []string
	}{
		{
			path: "toolchain/mir_generator.osty",
			needles: []string{
				"pub fn mirEmitModule(",
				"pub fn mirEmitOpts(",
				"pub struct MirEmitOpts ",
				// Phase 1a — multi-block walk + terminator dispatch.
				// The earlier Phase 0 emitter handled only a single
				// `entry:` stub; Phase 1a iterates `func.blocks` and
				// translates each MirTermKind. Loss of either the
				// per-block iteration or the terminator dispatch
				// would silently fall back to the old stub and
				// produce malformed IR for any function with > 1
				// block (loops, branches, …).
				"for block in func.blocks {",
				"mirEmitTerminatorLine(",
				"mirEmitBlockTargetLabel(",
				"MirTermReturn ->",
				"MirTermGoto ->",
				"MirTermUnreachable ->",
				// Phase 1b — operand resolution + real terminator
				// values. `MirTermReturn` now references
				// `func.returnLocal` via `%v<id>`, and `MirTermBranch`
				// reads its cond operand. Loss of these would silently
				// regress to the literal-zero / unreachable stubs.
				"pub fn mirEmitOperand(",
				"mirEmitReturnTerminator(",
				"mirEmitBranchTerminator(",
				"mirEmitLocalRegName(",
				"MirOpConst -> mirEmitConstOperand",
				"MirConstInt -> mirGenIntToString",
				// Phase 1c — per-instruction emission. The block walk
				// now visits `block.instrs` and dispatches each
				// MirInstrAssign through the rvalue arms (Use, Unary,
				// Binary covered today; rest stub-with-TODO). Loss of
				// either the walk hookup or the rvalue dispatch would
				// silently regress to the empty-body Phase 1b shape
				// where every fn produced ret-stub-only IR.
				"for instr in block.instrs {",
				// Phase 1c original signature; Phase 1e widened it
				// to (func, blockId, instrIdx, instr) so intrinsics
				// can mint deterministic temp names. Either shape
				// satisfies the dispatcher contract — assert just the
				// dispatcher name.
				"mirEmitInstruction(func, ",
				"mirEmitAssignInstr(",
				"mirEmitRValue(",
				"mirEmitRValueUse(",
				"mirEmitRValueUnary(",
				"mirEmitRValueBinary(",
				"mirBinaryOpSymbol(",
				"MirInstrAssign -> mirEmitAssignInstr",
				"MirRVBinary -> mirEmitRValueBinary",
				// Phase 1d — Call, Intrinsic, Aggregate (Tuple).
				// Loss of any of these would silently regress the
				// instruction dispatcher to its Phase 1c TODO stub.
				"mirEmitCallInstr(",
				"mirEmitCallReturnType(",
				"mirEmitCallArgList(",
				"mirEmitIntrinsicInstr(",
				"mirEmitIoWriteIntrinsic(",
				"mirEmitAbortIntrinsic(",
				"mirEmitRValueAggregate(",
				"mirEmitTupleAggregate(",
				"mirEmitTupleTypeText(",
				"MirInstrCall -> mirEmitCallInstr",
				"MirInstrIntrinsic -> mirEmitIntrinsicInstr",
				"MirIntrinsicPrintln -> mirEmitIoWriteIntrinsic",
				"MirAggTuple -> mirEmitTupleAggregate",
				// Phase 1e — parameter binding prologue, indirect
				// calls, typed print dispatch.
				"mirEmitParamBindingPrologue(func)",
				"mirEmitParamCopyLine(",
				"mirEmitToStringSymbol(",
				"mirEmitFreshTempName(",
				"mirEmitCallStmtLine(",
				"mirEmitCallValueLine(",
				"MirCalleeIndirect",
				"osty_rt_int_to_string",
				"osty_rt_bool_to_string",
				"osty_rt_float_to_string",
				"osty_rt_char_to_string",
				// Phase 1f — SwitchInt + Cast + Discriminant + Len.
				"mirEmitSwitchTerminator(",
				"mirEmitRValueCast(",
				"mirEmitRValueDiscriminant(",
				"mirEmitRValueLen(",
				"mirEmitIntResizeCastLine(",
				"mirEmitFloatResizeCastLine(",
				"mirEmitLenSymbol(",
				"MirTermSwitchInt -> mirEmitSwitchTerminator",
				"MirRVCast -> mirEmitRValueCast",
				"MirRVDiscriminant -> mirEmitRValueDiscriminant",
				"MirRVLen -> mirEmitRValueLen",
				"MirCastIntResize ->",
				"MirCastIntToFloat ->",
				"MirCastBitcast ->",
				"osty_rt_list_len",
				"osty_rt_map_len",
				"osty_rt_set_len",
				// Phase 1g — single-step projection chains in
				// READ-side rvalues (Use of projected operand).
				// Loss of any of these would silently regress to
				// the Phase 1f stub-`0` operand path.
				"mirOperandHasProjections(",
				"mirEmitProjectedRead(",
				"mirEmitProjectFieldLine(",
				"mirEmitProjectVariantLine(",
				"mirEmitProjectIndexLine(",
				"mirEmitListGetSymbol(",
				"MirProjField -> mirEmitProjectFieldLine",
				"MirProjTuple -> mirEmitProjectFieldLine",
				"MirProjIndex -> mirEmitProjectIndexLine",
				"MirProjDeref ->",
				"osty_rt_list_get_i64",
				"osty_rt_list_get_ptr",
				"osty_rt_list_get_f64",
				// Phase 1h — Struct + EnumVariant aggregates.
				"mirEmitStructAggregate(",
				"mirEmitEnumVariantAggregate(",
				"MirAggStruct -> mirEmitStructAggregate",
				"MirAggEnumVariant -> mirEmitEnumVariantAggregate",
				// Phase 1i — more intrinsics.
				"mirEmitListPushIntrinsic(",
				"mirEmitListGetIntrinsic(",
				"mirEmitStringPredicateIntrinsic(",
				"mirEmitListPushSymbol(",
				"MirIntrinsicListPush -> mirEmitListPushIntrinsic",
				"MirIntrinsicListGet -> mirEmitListGetIntrinsic",
				"MirIntrinsicStringContains -> mirEmitStringPredicateIntrinsic",
				"MirIntrinsicStringStartsWith -> mirEmitStringPredicateIntrinsic",
				"MirIntrinsicStringEndsWith -> mirEmitStringPredicateIntrinsic",
				"osty_rt_list_push_i64",
				"osty_rt_list_push_ptr",
				"osty_rt_strings_Contains",
				"osty_rt_strings_HasPrefix",
				"osty_rt_strings_HasSuffix",
				// Phase 1j — module-level sections.
				"pub fn mirEmitRuntimeDeclarationsSection(",
				"pub fn mirEmitGlobalsSection(",
				"mirEmitRuntimeDeclarationsSection()",
				"mirEmitGlobalsSection(m)",
				"mirEmitGlobalLine(",
				"mirEmitGlobalLLVMType(",
				"mirEmitGlobalInitializer(",
				"declare void @osty_rt_io_write",
				"declare ptr @osty_rt_int_to_string",
				"declare i64 @osty_rt_list_len",
				// Phase 1k mass push — multi-step projections,
				// WRITE projection, Optional casts, Ref/AddressOf/
				// GlobalRef/Nullary rvalues, multi-field enum
				// payloads, expanded Map/Set/List/String intrinsics.
				"mirEmitProjectedReadChain(",
				"mirEmitProjectionStepLine(",
				"mirEmitAssignIntoProjection(",
				"mirProjectionKindName(",
				"mirEmitOptionalWrapCast(",
				"mirEmitOptionalUnwrapCast(",
				"mirEmitWidenPayloadLine(",
				"mirEmitNarrowPayloadLine(",
				"mirEmitRValueRef(",
				"mirEmitRValueGlobalRef(",
				"mirEmitRValueNullary(",
				"mirEmitContainerLenIntrinsic(",
				"mirEmitListIsEmptyIntrinsic(",
				"mirEmitListReverseIntrinsic(",
				"mirEmitMapClearIntrinsic(",
				"mirEmitContainerCopyIntrinsic(",
				"mirEmitStringLenIntrinsic(",
				"mirEmitStringIsEmptyIntrinsic(",
				"mirEmitStringIndexOfIntrinsic(",
				"mirEmitStringUnaryIntrinsic(",
				"mirEmitStringReplaceIntrinsic(",
				"MirRVRef -> mirEmitRValueRef",
				"MirRVAddressOf -> mirEmitRValueRef",
				"MirRVGlobalRef -> mirEmitRValueGlobalRef",
				"MirRVNullary -> mirEmitRValueNullary",
				"MirCastOptionalWrap -> mirEmitOptionalWrapCast",
				"MirCastOptionalUnwrap -> mirEmitOptionalUnwrapCast",
				"MirNullaryNone ->",
				"MirIntrinsicListIsEmpty -> mirEmitListIsEmptyIntrinsic",
				"MirIntrinsicMapKeys -> mirEmitContainerCopyIntrinsic",
				"MirIntrinsicMapValues -> mirEmitContainerCopyIntrinsic",
				"MirIntrinsicMapClear -> mirEmitMapClearIntrinsic",
				"MirIntrinsicSetToList -> mirEmitContainerCopyIntrinsic",
				"MirIntrinsicStringLen -> mirEmitStringLenIntrinsic",
				"MirIntrinsicStringTrim -> mirEmitStringUnaryIntrinsic",
				"MirIntrinsicStringToUpper -> mirEmitStringUnaryIntrinsic",
				"MirIntrinsicStringToLower -> mirEmitStringUnaryIntrinsic",
				"MirIntrinsicStringReplace -> mirEmitStringReplaceIntrinsic",
				"MirIntrinsicStringIndexOf -> mirEmitStringIndexOfIntrinsic",
				"declare ptr @osty_rt_strings_TrimSpace",
				"declare ptr @osty_rt_strings_ToUpper",
				"declare ptr @osty_rt_strings_ToLower",
				"declare ptr @osty_rt_strings_ReplaceAll",
				"declare ptr @osty_rt_map_keys",
				"declare ptr @osty_rt_set_to_list",
				// Phase 1l mass push 2 — closes intrinsic catalog
				// for String/List/Map/Set/Channel/Spawn + lifetime.
				"mirEmitStringSplitIntrinsic(",
				"mirEmitStringToIntIntrinsic(",
				"mirEmitStringToFloatIntrinsic(",
				"mirEmitStringCountIntrinsic(",
				"mirEmitStringConcatIntrinsic(",
				"mirEmitListEdgeIntrinsic(",
				"mirEmitListIndexOfIntrinsic(",
				"mirEmitListSortedIntrinsic(",
				"mirEmitListReversedIntrinsic(",
				"mirEmitListToSetIntrinsic(",
				"mirEmitListRemoveAtIntrinsic(",
				"mirEmitListToStringIntrinsic(",
				"mirEmitMapToStringIntrinsic(",
				"mirEmitMapKeysSortedIntrinsic(",
				"mirEmitMapContainsIntrinsic(",
				"mirEmitMapRemoveIntrinsic(",
				"mirEmitSetInsertIntrinsic(",
				"mirEmitSetContainsIntrinsic(",
				"mirEmitSetRemoveIntrinsic(",
				"mirEmitSetToStringIntrinsic(",
				"mirEmitChanMakeIntrinsic(",
				"mirEmitChanSendIntrinsic(",
				"mirEmitChanRecvIntrinsic(",
				"mirEmitChanCloseIntrinsic(",
				"mirEmitChanIsClosedIntrinsic(",
				"mirEmitSpawnIntrinsic(",
				"mirEmitHandleJoinIntrinsic(",
				"mirEmitYieldIntrinsic(",
				"mirEmitSleepIntrinsic(",
				"mirEmitIsCancelledIntrinsic(",
				"mirEmitCheckCancelledIntrinsic(",
				"mirEmitStorageLifetimeLine(",
				"MirIntrinsicListFirst -> mirEmitListEdgeIntrinsic",
				"MirIntrinsicListLast -> mirEmitListEdgeIntrinsic",
				"MirIntrinsicListIndexOf -> mirEmitListIndexOfIntrinsic",
				"MirIntrinsicListSorted -> mirEmitListSortedIntrinsic",
				"MirIntrinsicMapContains -> mirEmitMapContainsIntrinsic",
				"MirIntrinsicMapRemove -> mirEmitMapRemoveIntrinsic",
				"MirIntrinsicSetInsert -> mirEmitSetInsertIntrinsic",
				"MirIntrinsicSetContains -> mirEmitSetContainsIntrinsic",
				"MirIntrinsicChanMake -> mirEmitChanMakeIntrinsic",
				"MirIntrinsicChanSend -> mirEmitChanSendIntrinsic",
				"MirIntrinsicChanRecv -> mirEmitChanRecvIntrinsic",
				"MirIntrinsicSpawn -> mirEmitSpawnIntrinsic",
				"MirIntrinsicHandleJoin -> mirEmitHandleJoinIntrinsic",
				"MirIntrinsicYield -> mirEmitYieldIntrinsic",
				"MirIntrinsicSleep -> mirEmitSleepIntrinsic",
				"MirInstrStorageLive -> mirEmitStorageLifetimeLine",
				"MirInstrStorageDead -> mirEmitStorageLifetimeLine",
				"declare ptr @osty_rt_thread_chan_make",
				"declare void @osty_rt_thread_chan_send_i64",
				"declare \\{ i64, i64 \\} @osty_rt_thread_chan_recv_i64",
				"declare ptr @osty_rt_task_spawn",
				"declare void @osty_rt_yield",
				"declare void @osty_rt_sleep",
				"declare ptr @osty_rt_strings_Concat",
				"declare ptr @osty_rt_strings_Split",
				"declare i64 @osty_rt_strings_ToInt",
				"declare double @osty_rt_strings_ToFloat",
				"declare ptr @osty_rt_list_sorted_i64",
				"declare ptr @osty_rt_list_reversed",
				"declare ptr @osty_rt_list_to_set_i64",
				"declare void @osty_rt_list_remove_at_discard",
				"declare ptr @osty_rt_list_to_string_i64",
				"declare ptr @osty_rt_map_to_string",
				"declare ptr @osty_rt_map_keys_sorted_i64",
				"declare i1 @osty_rt_map_contains_i64",
				"declare i1 @osty_rt_set_insert_i64",
				"declare ptr @osty_rt_set_to_string",
				"declare void @llvm.lifetime.start.p0",
				"declare void @llvm.lifetime.end.p0",
				// Phase 1m — closures, task family, multi-step
				// write proj, bytes, set per-elem dispatch.
				"mirEmitClosureAggregate(",
				"mirEmitListAggregate(",
				"mirEmitMapAggregate(",
				"mirEmitTaskGroupIntrinsic(",
				"mirEmitGroupCancelIntrinsic(",
				"mirEmitGroupIsCancelledIntrinsic(",
				"mirEmitParallelIntrinsic(",
				"mirEmitRaceIntrinsic(",
				"mirEmitCollectAllIntrinsic(",
				"mirEmitSelectIntrinsic(",
				"mirEmitSelectRecvIntrinsic(",
				"mirEmitSelectSendIntrinsic(",
				"mirEmitSelectTimeoutIntrinsic(",
				"mirEmitSelectDefaultIntrinsic(",
				"mirEmitBytesIsEmptyIntrinsic(",
				"mirEmitBytesGetIntrinsic(",
				"mirEmitBytesIndexOfIntrinsic(",
				"mirEmitBytesSplitIntrinsic(",
				"mirEmitAssignIntoSingleProjection(",
				"mirSetInsertSymbolFor(",
				"MirAggClosure -> mirEmitClosureAggregate",
				"MirAggList -> mirEmitListAggregate",
				"MirAggMap -> mirEmitMapAggregate",
				"MirIntrinsicTaskGroup -> mirEmitTaskGroupIntrinsic",
				"MirIntrinsicParallel -> mirEmitParallelIntrinsic",
				"MirIntrinsicRace -> mirEmitRaceIntrinsic",
				"MirIntrinsicCollectAll -> mirEmitCollectAllIntrinsic",
				"MirIntrinsicSelect -> mirEmitSelectIntrinsic",
				"MirIntrinsicSelectRecv -> mirEmitSelectRecvIntrinsic",
				"MirIntrinsicBytesLen -> mirEmitContainerLenIntrinsic",
				"MirIntrinsicBytesGet -> mirEmitBytesGetIntrinsic",
				"MirIntrinsicBytesSplit -> mirEmitBytesSplitIntrinsic",
				"declare i64 @osty_rt_task_group",
				"declare ptr @osty_rt_parallel",
				"declare \\{ i64, i64 \\} @osty_rt_task_race",
				"declare ptr @osty_rt_task_collect_all",
				"declare void @osty_rt_select",
				"declare i8 @osty_rt_bytes_get",
				"declare i64 @osty_rt_bytes_index_of",
				"declare ptr @osty_rt_bytes_split",
				"declare ptr @osty_rt_make_closure",
				"declare ptr @osty_rt_list_new",
				"declare ptr @osty_rt_map_new",
				// Phase 1n — variant per-field unwrap, list contains
				// via indexOf, set/map per-elem expansion, module
				// init dispatcher.
				"mirEmitProjectVariantLineFromHelper(",
				"mirSetRemoveSymbolFor(",
				"mirSetContainsSymbolFor(",
				"mirEmitMapSetIntrinsic(",
				"mirEmitMapGetIntrinsic(",
				"mirMapInsertSymbolFor(",
				"mirMapGetSymbolFor(",
				"mirEmitModuleInitDispatcher(",
				"MirIntrinsicMapSet -> mirEmitMapSetIntrinsic",
				"MirIntrinsicMapGet -> mirEmitMapGetIntrinsic",
				"mirEmitModuleInitDispatcher(m)",
				"@osty_module_init",
				"declare void @osty_rt_map_insert_i64",
				"declare void @osty_rt_map_insert_string",
				"declare i1 @osty_rt_map_get_i64",
				"declare i1 @osty_rt_map_get_string",
				"declare i1 @osty_rt_set_remove_ptr",
				"declare i1 @osty_rt_set_contains_i1",
				// Phase 1o — string pool emit + type defs section
				// + Index/Deref WRITE projection.
				"pub fn mirEmitStringPoolSection(",
				"pub fn mirEmitTypeDefsSection(",
				"pub fn mirEmitIndexOrDerefWrite(",
				"mirEncodeStringLiteral(",
				"mirHexEscape(",
				"mirHexDigit(",
				"mirEmitIndexWriteLine(",
				"mirEmitDerefWriteLine(",
				"mirEmitListSetSymbol(",
				"mirEmitTypeDefsSection(m)",
				"pub fn mirEmitStringPoolSection(",
				"declare void @osty_rt_list_set_i64",
				"declare void @osty_rt_list_set_ptr",
				// Phase 1p — string pool actually populated, type
				// defs section actually populated, multi-step
				// Index/Deref WRITE projection.
				"pub fn mirEmitConstStringRef(",
				"pub fn mirStringRefSymbol(",
				"pub fn mirCollectStringPool(",
				"pub fn mirCollectAggregateShapes(",
				"mirFnv1aHash32(",
				"mirHexEncodeU32(",
				"mirCollectStringPoolFromInstr(",
				"mirCollectStringPoolFromOperand(",
				"mirEmitMultiStepIndexWrite(",
				"mirEmitMultiStepDerefWrite(",
				"MirConstString -> mirEmitConstStringRef",
				"mirCollectStringPool(m)",
				"mirEmitStringPoolSection(pool)",
				"mirCollectAggregateShapes(m)",
				"@.str.\" + mirHexEncodeU32(mirFnv1aHash32",
				// Phase 2a — straight-line intrinsic emission for
				// the large runtime-backed tail of the MIR catalog.
				"mirEmitMapNewIntrinsic(",
				"mirEmitSetNewIntrinsic(",
				"mirEmitListInsertIntrinsic(",
				"mirEmitListClearIntrinsic(",
				"mirEmitListSliceIntrinsic(",
				"mirEmitStringSplitNIntrinsic(",
				"mirEmitStringNthSegmentIntrinsic(",
				"mirEmitBytesLastIndexOfIntrinsic(",
				"mirEmitBytesFromStringIntrinsic(",
				"mirEmitAlgebraicPredicateIntrinsic(",
				"mirEmitPrimitiveConversionIntrinsic(",
				"MirIntrinsicBytesConcat -> mirEmitBytesBinaryIntrinsic",
				"MirIntrinsicOptionIsSome -> mirEmitAlgebraicPredicateIntrinsic",
				"MirIntrinsicRawNull -> mirEmitRawNullIntrinsic",
				"MirIntrinsicMapGetOr -> mirEmitMapGetOrIntrinsic",
				"declare ptr @osty_rt_bytes_concat",
				"declare ptr @osty_rt_bytes_from_hex",
				"declare ptr @osty_rt_strings_SplitN",
				"declare ptr @osty_rt_set_new",
				// Phase 2b — control-flow-backed List option
				// probes and scalar scans.
				"mirEmitIntrinsicAuxLabel(",
				"mirEmitOptionSomeToSlotLines(",
				"mirEmitOptionNoneToSlotLines(",
				"mirEmitListOptionProbeIntrinsic(",
				"mirEmitListScanCompareLine(",
				"MirIntrinsicListFirst -> mirEmitListEdgeIntrinsic(func, blockId, instrIdx, instr, true)",
				"MirIntrinsicListIndexOf -> mirEmitListIndexOfIntrinsic(func, blockId, instrIdx, instr)",
				"MirIntrinsicListContains -> mirEmitListContainsIntrinsic(func, blockId, instrIdx, instr)",
				"MirIntrinsicListPop -> mirEmitListPopIntrinsic(func, blockId, instrIdx, instr)",
				"declare i1 @osty_rt_strings_Equal",
				// Phase 2c — Bytes.get returns Option<Byte>
				// instead of a naked byte load.
				"MirIntrinsicBytesGet -> mirEmitBytesGetIntrinsic(func, blockId, instrIdx, instr)",
				"mirEmitIntrinsicAuxLabel(blockId, instrIdx, \"bytes.get.some\")",
				"mirEmitOptionSomeToSlotLines(base + \".some\", optLLVM, \"i8\", byteReg, resultSlot)",
				// Phase 2d — unwrap checks the absent tag before
				// narrowing payloads.
				"MirIntrinsicOptionUnwrap -> mirEmitAlgebraicUnwrapIntrinsic(func, blockId, instrIdx, instr, mirDiscriminantNone(), \"osty_rt_option_unwrap_none\")",
				"MirIntrinsicResultUnwrap -> mirEmitAlgebraicUnwrapIntrinsic(func, blockId, instrIdx, instr, mirDiscriminantErr(), \"osty_rt_result_unwrap_err\")",
				"mirEmitIntrinsicAuxLabel(blockId, instrIdx, \"algebraic.unwrap.absent\")",
				"declare void @osty_rt_option_unwrap_none()",
				"declare void @osty_rt_result_unwrap_err()",
				// Phase 2e — taskGroup result payloads use the same
				// i64 raw lane narrowing as handle.join.
				"return out + mirEmitNarrowPayloadLine(dest, raw, destLLVM)",
				"TODO taskGroup dest type",
			},
		},
		{
			path: "toolchain/mir_lower.osty",
			needles: []string{
				// hirMonomorphizeModule must still be invoked from
				// the MIR lowerer — Phase 0 leaves this wiring intact
				// rather than re-routing it. Loss of this call would
				// silently drop monomorphization from the chain.
				"hirMonomorphizeModule(",
			},
		},
		{
			path: "toolchain/monomorph_pass.osty",
			needles: []string{
				"pub fn hirMonomorphizeModule(",
			},
		},
		{
			path: "toolchain/main.osty",
			needles: []string{
				`hasSelfhostCommand(args, "compile")`,
				"runCompile(args)",
				"hirLowerSource(",
				"mirLowerModule(",
				"mirEmitModule(",
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.path, func(t *testing.T) {
			full := filepath.Join(root, tc.path)
			src, err := os.ReadFile(full)
			if err != nil {
				t.Fatalf("read %s: %v", tc.path, err)
			}
			text := string(src)
			for _, needle := range tc.needles {
				if !strings.Contains(text, needle) {
					t.Errorf("missing wiring %q in %s", needle, tc.path)
				}
			}
		})
	}
}

// TestSelfhostDoctorRunsSourceProbe pins the `--selfhost-doctor`
// source probe so the status cannot drift back to a text-only Phase 0
// claim while the real LIR Proto path regresses underneath it.
func TestSelfhostDoctorRunsSourceProbe(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(root, "toolchain", "main.osty"))
	if err != nil {
		t.Fatalf("read main.osty: %v", err)
	}
	text := string(src)
	// The doctor must NOT keep saying "missing stage: MIR-to-LLVM
	// module emitter" once LIR Proto lowering is wired. It SHOULD run a
	// small source pipeline and only mark the source compiler enabled
	// when that probe reaches rendered LLVM IR.
	regressed := regexp.MustCompile(`missing stage:\s*MIR-to-LLVM module emitter`)
	if regressed.MatchString(text) {
		t.Fatalf("doctor still claims module emitter is missing; LIR Proto lowering should have flipped this status")
	}
	for _, needle := range []string{
		"selfhostProbeError",
		"lirLowerMirModule(",
		"lirRenderModule(",
		"LLVM IR renderer produced no return instruction",
		"osty-self source compiler: enabled",
		"osty-self self-rebuild probe: OK",
		"full toolchain directory rebuild/link orchestration",
	} {
		if !strings.Contains(text, needle) {
			t.Errorf("doctor probe missing %q", needle)
		}
	}
}
