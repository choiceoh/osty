package selfhost

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestPhase0SelfHostWiringExists guards the self-host backend wiring:
// the legacy MIR emitter remains available in `toolchain/mir_generator.osty`,
// while `toolchain/main.osty` dispatches `compile <file>` through the
// same LIR Proto path that `--selfhost-doctor` probes.
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
// The historical test name is kept so old targeted test commands keep
// working, but the guarded production path is now source → HIR → MIR →
// LIR Proto → LLVM IR.
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
				// Phase 3a/3b — function-level attribute string +
				// param noalias threaded into the `define` header.
				// `mirEmitFunctionStub` was passing `""` for attrs
				// even though `mirFormatFnAttrs` was already
				// implemented; Phase 3a wires A8/A9/A10/A13 through
				// `mirEmitFnAttrsFromMir`, and Phase 3b extends
				// `mirEmitParamListJoined` with the `noalias` param
				// attribute decided by `mirParamIsNoalias`. Loss of
				// any of these needles would silently regress the
				// emitter back to attribute-free + alias-able
				// signatures.
				"pub fn mirInlineModeDiscriminant(",
				"mirEmitFnAttrsFromMir(",
				"mirEmitParamLocalName(",
				"let attrs = mirEmitFnAttrsFromMir(func)",
				"mirFunctionDefineHeader(\"\", func.returnType, func.name, paramListJoined, attrs)",
				"mirParamIsNoalias(ty, nameStr, func.noaliasAll, func.noaliasParams)",
				"MirInlineNone -> 0",
				"MirInlineSoft -> 1",
				"MirInlineAlways -> 2",
				"MirInlineNever -> 3",
				// Phase 3c — function-entry GC safepoint. Every
				// emitted `define` block opens with
				// `call void @osty.gc.safepoint_v1(i64 <entry-id>,
				// ptr null, i64 0)` (encoded `1 << 56 =
				// 72057594037927936` per lir_proto_plan.md Phase 5),
				// matching the production Go emitter so the GC can
				// observe entered functions regardless of which MIR
				// block was tagged as `fn_.entry`. The matching
				// runtime decl is threaded into
				// `mirEmitRuntimeDeclarationsSection` via
				// `mirRuntimeDeclareSafepointV1`. Loss of any of
				// these needles silently regresses the emit to
				// safepoint-free bodies that miss the GC observation
				// window and produce a dangling call.
				"pub fn mirEntrySafepointIdString(",
				"pub fn mirEmitEntryGcSafepoint(",
				"\"72057594037927936\"",
				"mirEmitEntryGcSafepoint()",
				"mirRuntimeDeclareSafepointV1()",
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
				`hasSelfhostCommand(args, "lir-proto-lower-mir-json")`,
				"runLirProtoLowerMirJson(args)",
				"hirLowerSource(",
				"mirLowerModule(",
				`lirLowerConfig("main", path, "")`,
				"lirLowerMirModule(cfg, checkedMir)",
				"lirRenderModule(result.module)",
			},
		},
		{
			path: "toolchain/mir_json.osty",
			needles: []string{
				"pub fn mirJsonParseModule(",
				"fn mirJsonModule(",
				"mirJsonFunction(",
				"mirJsonInstr(",
				"mirJsonRValue(",
				"mirJsonLayouts(",
				"fn mirJsonLayoutsInto(value: MirJsonValue, out: MirLayoutTable) -> Result<Bool, String>",
				"fn mirJsonLayoutStructsInto(items: List<MirJsonValue>, pos: Int, out: MirLayoutTable) -> Result<Bool, String>",
				"fn mirJsonModuleObjectInto(obj: List<MirJsonEntry>, m: MirModule) -> Result<Bool, String>",
				"Ok(MirUse {rawPath, alias, isGoFFI, isRuntimeFFI, goPath, runtimePath, span})",
				"Ok(MirGlobal {name, typ, mutable, hasInit, initSymbol, span})",
				"Ok(MirLocal {id, name, typ, mutable, isParam, isReturn, span})",
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
			needles := tc.needles
			if tc.path == "toolchain/mir_generator.osty" {
				// `compile` no longer depends on the legacy direct emitter's
				// per-instruction catalog. Keep a smoke guard that the legacy
				// entrypoints still exist, and let LIR Proto tests own the
				// production self-host path.
				needles = []string{
					"pub fn mirEmitModule(",
					"pub fn mirEmitOpts(",
					"pub struct MirEmitOpts ",
					"mirEmitFunctionStub(",
				}
			}
			for _, needle := range needles {
				if !strings.Contains(text, needle) {
					t.Errorf("missing wiring %q in %s", needle, tc.path)
				}
			}
		})
	}
}

// TestSelfhostDoctorRunsBackendProbe pins the `--selfhost-doctor`
// backend probe so the status cannot drift back to a text-only Phase 0
// claim while the real MIR JSON -> LIR Proto path regresses underneath it.
func TestSelfhostDoctorRunsBackendProbe(t *testing.T) {
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
		"mirJsonParseModule(raw)",
		"selfhostMirProbeError",
		"lirLowerMirModuleListsInto(",
		"LLVM IR renderer produced no return instruction",
		"selfRebuildBundleSource",
		"selfRebuildRunClang",
		"osty-self MIR JSON backend: enabled",
		"osty-self self-rebuild probe: OK",
		"osty-self self-rebuild driver: MIR JSON backend -> clang object/runtime link",
	} {
		if !strings.Contains(text, needle) {
			t.Errorf("doctor probe missing %q", needle)
		}
	}
}

func TestElabInferLoopExprGuardStaysOutsideKindMatch(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(root, "toolchain", "elab.osty"))
	if err != nil {
		t.Fatalf("read elab.osty: %v", err)
	}
	text := string(src)
	if strings.Contains(text, "_ if node.kind == AstNFor && node.text == \"loopexpr\"") {
		t.Fatalf("elabInferImpl reintroduced guarded match arm; stage0 lowers that fallback to unreachable")
	}
	guard := "if node.kind == AstNFor && node.text == \"loopexpr\""
	guardPos := strings.Index(text, guard)
	if guardPos < 0 {
		t.Fatalf("elabInferImpl missing bootstrap-safe loopexpr guard")
	}
	matchPos := strings.Index(text[guardPos:], "match node.kind")
	if matchPos < 0 {
		t.Fatalf("elabInferImpl missing node.kind match after loopexpr guard")
	}
}

func TestMirLowerEmitScriptSkipsSyntheticMainWhenExplicitMainExists(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(root, "toolchain", "mir_lower.osty"))
	if err != nil {
		t.Fatalf("read mir_lower.osty: %v", err)
	}
	text := string(src)
	fnPos := strings.Index(text, "pub fn mirLowerEmitScript")
	if fnPos < 0 {
		t.Fatalf("mirLowerEmitScript missing")
	}
	body := text[fnPos:]
	guardPos := strings.Index(body, `if existing.name == "main"`)
	mainPos := strings.Index(body, `mirFunction("main"`)
	if guardPos < 0 {
		t.Fatalf("mirLowerEmitScript missing explicit-main guard")
	}
	if mainPos < 0 {
		t.Fatalf("mirLowerEmitScript missing synthetic main construction")
	}
	if guardPos > mainPos {
		t.Fatalf("mirLowerEmitScript checks explicit main after constructing synthetic main")
	}
}

func TestSelfhostDoctorProbeSourceEscapesLiteralBraces(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	for _, path := range []string{
		filepath.Join(root, "toolchain", "main.osty"),
		filepath.Join(root, "toolchain", "selfhost_driver.osty"),
	} {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		text := string(src)
		if strings.Contains(text, `fn main() -> Int {\n`) {
			t.Fatalf("%s has an unescaped probe source brace; self-host string interpolation corrupts the doctor probe", path)
		}
		if !strings.Contains(text, `fn main() -> Int \{\n`) {
			t.Fatalf("%s missing escaped self-host doctor probe source", path)
		}
		if !strings.Contains(text, `\}\n"`) {
			t.Fatalf("%s missing escaped closing brace in self-host doctor probe source", path)
		}
	}
}

func TestMirLowerTypeArgHelpersUseBootstrapSafeEarlyReturns(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(root, "toolchain", "mir_lower.osty"))
	if err != nil {
		t.Fatalf("read mir_lower.osty: %v", err)
	}
	text := string(src)
	for _, fn := range []string{"hirTypeFirstArg", "hirTypeSecondArg"} {
		start := strings.Index(text, "pub fn "+fn)
		if start < 0 {
			t.Fatalf("%s missing", fn)
		}
		end := strings.Index(text[start+1:], "\npub fn ")
		if end < 0 {
			t.Fatalf("%s body boundary missing", fn)
		}
		body := text[start : start+1+end]
		if strings.Contains(body, "match t.kind") {
			t.Fatalf("%s reintroduced match-expression shape; stage0 lowered its success arm to unreachable", fn)
		}
		if !strings.Contains(body, "return hirTypeInvalid()") {
			t.Fatalf("%s missing early invalid return", fn)
		}
		if !strings.Contains(body, "t.namedArgs[") {
			t.Fatalf("%s missing namedArgs projection", fn)
		}
	}
	for _, bad := range []string{
		"fn hirTypeContainsVar(t: HirType) -> Bool {\n    match t.kind",
		"fn hirTypeHasErrArg(t: HirType) -> Bool {\n    match t.kind",
		"pub fn mirLowerResultErrType(t: HirType) -> HirType {\n    match t.kind",
		"pub fn mirLowerOptionInnerType(t: HirType) -> HirType {\n    match t.kind",
		"fn mirLowerResultOkType(t: HirType) -> HirType {\n    match t.kind",
		"pub fn mirLowerTupleElementTypeAt(t: HirType, idx: Int) -> HirType {\n    match t.kind",
	} {
		if strings.Contains(text, bad) {
			t.Fatalf("mir_lower.osty reintroduced bootstrap-unsafe type helper shape: %q", bad)
		}
	}
	for _, good := range []string{
		"return t.optionalInner[0]",
		"return t.namedArgs[0]",
		"return t.namedArgs[1]",
		"t.tupleElems[idx]",
	} {
		if !strings.Contains(text, good) {
			t.Fatalf("mir_lower.osty missing bootstrap-safe helper projection %q", good)
		}
	}
}

func TestMirGeneratorLenRVUsesPlaceContainerType(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(root, "toolchain", "mir_generator.osty"))
	if err != nil {
		t.Fatalf("read mir_generator.osty: %v", err)
	}
	text := string(src)
	for _, needle := range []string{
		"let containerType = mirEmitLenContainerType(func, rv.place, rv.typ)",
		"let lenSym = mirEmitLenSymbol(containerType)",
		"mirEmitModuleProjectedReadChain(m, func, block, instrIdx, tmp, rv.place, containerType)",
		"fn mirEmitLenContainerType(func: MirFunction, place: MirPlace, fallback: String) -> String",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("mir_generator.osty missing LenRV container-type wiring %q", needle)
		}
	}
	if strings.Contains(text, "let lenSym = mirEmitLenSymbol(rv.typ)") {
		t.Fatalf("mir_generator.osty still treats LenRV result type as the container type")
	}
}

func TestMirGeneratorDominatingValueSkipsNonDefiningBackedges(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(root, "toolchain", "mir_generator.osty"))
	if err != nil {
		t.Fatalf("read mir_generator.osty: %v", err)
	}
	text := string(src)
	needle := "if pred.id >= blockId && !mirBlockHasLocalValueAtEnd(pred, local) {\n                continue\n            }"
	if strings.Count(text, needle) < 2 {
		t.Fatalf("mir_generator.osty missing non-defining backedge skip in dominating local value helpers")
	}
}

func TestMirGeneratorIndexWritesDoNotDefineProjectionSSAValue(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(root, "toolchain", "mir_generator.osty"))
	if err != nil {
		t.Fatalf("read mir_generator.osty: %v", err)
	}
	text := string(src)
	for _, needle := range []string{
		"let last = instr.dest.projections[instr.dest.projections.len() - 1]",
		"if last.kind == MirProjIndex || last.kind == MirProjDeref",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("mir_generator.osty missing index/deref projection-write guard %q", needle)
		}
	}
}

func TestMirGeneratorProjectionWriteTempsUseBlockQualifiedNames(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(root, "toolchain", "mir_generator.osty"))
	if err != nil {
		t.Fatalf("read mir_generator.osty: %v", err)
	}
	text := string(src)
	for _, needle := range []string{
		"mirProjectionWriteTargetName(block, instrIdx, instr.dest.local) + \".wv\"",
		"let midRead = finalTarget + \".r0\"",
		"let innerWrite = finalTarget + \".w1\"",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("mir_generator.osty missing block-qualified projection temp %q", needle)
		}
	}
}

func TestMirLowerStage2SeedDiscardsStatementCalls(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(root, "toolchain", "mir_lower.osty"))
	if err != nil {
		t.Fatalf("read mir_lower.osty: %v", err)
	}
	text := string(src)
	for _, needle := range []string{
		"if s.exprValue.kind == HirExprCall",
		"mirLowerCallExprInto(bs, s.exprValue, mirPlace(-1), hirTUnit())",
		"if s.exprValue.kind == HirExprMethod",
		"mirLowerMethodCallInto(bs, s.exprValue, mirPlace(-1), hirTUnit())",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("mir_lower.osty missing statement-call discard guard %q", needle)
		}
	}
}

func TestMirConstructorsAvoidProjectedScalarAssigns(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(root, "toolchain", "mir.osty"))
	if err != nil {
		t.Fatalf("read mir.osty: %v", err)
	}
	text := string(src)
	for _, needle := range []string{
		"MirConst {kind: MirConstInt",
		"MirProjection {kind: MirProjField",
		"MirOperand {kind: MirOpCopy",
		"MirRValue {kind: MirRVAggregate",
		"MirRValue {kind: MirRVNullary",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("mir.osty missing direct constructor literal %q", needle)
		}
	}
	for _, forbidden := range []string{
		"let mut c = mirConstInvalid()",
		"let mut p = mirProjInvalid()",
		"let mut op = mirOperandInvalid()",
		"let mut rv = mirRValueInvalid()",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("mir.osty reintroduced projected scalar constructor assignment %q", forbidden)
		}
	}
}

func TestMirJsonStage2SeedAvoidsProjectedScalarAssigns(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(root, "toolchain", "mir_json.osty"))
	if err != nil {
		t.Fatalf("read mir_json.osty: %v", err)
	}
	text := string(src)
	for _, forbidden := range []string{
		"m.packageName =",
		"m.span =",
		"u.isGoFFI =",
		"u.isRuntimeFFI =",
		"u.goPath =",
		"u.runtimePath =",
		"g.hasInit =",
		"g.initSymbol =",
		"l.isParam =",
		"l.isReturn =",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("mir_json.osty reintroduced projected scalar assignment %q", forbidden)
		}
	}
}

func TestLirProtoStage2SeedCoercionAndIndexStoreGuards(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(root, "toolchain", "lir_proto.osty"))
	if err != nil {
		t.Fatalf("read lir_proto.osty: %v", err)
	}
	text := string(src)
	for _, needle := range []string{
		"fn lirStoreIndexedListValue(l: LirMirFunctionLowerer, place: MirPlace, root: MirLocal, rootType: LirType, value: LirOperand, valueMirType: String, context: String)",
		"llvmListRuntimeSetSymbolFor(elemLane.llvm, lirContainerElemIsString(elemName))",
		"mirRtListSymbol(\"set_bytes_v1\")",
		"fn lirCoerceValueToType(l: LirMirFunctionLowerer, value: LirOperand, valueMirType: String, destTypeName: String, destType: LirType, context: String) -> LirOperand",
		"fn lirRebuildDefaultI64Aggregate(l: LirMirFunctionLowerer, value: LirOperand, destType: LirType) -> LirOperand",
		"fn lirEmitEnumDiscriminantAggregate(l: LirMirFunctionLowerer, discValue: LirOperand, destType: LirType) -> LirOperand",
		"lirCoerceTargetIsPayloadEnum(l, destTypeName)",
		"fn lirAppendRenderedLirBlocks(renderedBlocks: List<String>, blocks: List<LirBlock>, pos: Int)",
		"lirAppendRenderedLirBlocks(renderedBlocks, blockLowerer.extraBlocks, 0)",
		"fn lirLowerMirListGetSafe(l: LirMirFunctionLowerer, instr: MirInstr, optTypeName: String)",
		"lirLowerMirListGetSafe(l, instr, effectiveTypeName)",
		"fn lirBinaryAddIsStringConcat(rv: MirRValue, hintName: String, hint: LirType) -> Bool",
		"left = lirCoerceValueToType(l, left, rv.arg.typ, \"String\", lirPtrType(), \"string concat left\")",
		"fn lirBinaryIsStringComparison(rv: MirRValue) -> Bool",
		"fn lirLowerMirStringComparison(l: LirMirFunctionLowerer, rv: MirRValue) -> LirOperand",
		"lirRuntimeDeclI1FromTwoPtr(symbol)",
		"lirRuntimeDeclI64FromTwoPtr(symbol)",
		"l.instrs.push(lirCall(eqDest, boolType, \"@\" + symbol",
		"l.instrs.push(lirBinary(neDest, \"xor\", boolType, eqDest, \"1\"))",
		"out = strings.replaceAll(out, \"<\", \".\")",
		"out = strings.replaceAll(out, \" \", \"\")",
		"fn lirEncodedCStringByteLen(encoded: String) -> Int",
		"lirEncodedCStringByteLen(encoded)",
		"fn lirNoTypeParams() -> List<LirType>",
		"fn lirNoOperands() -> List<LirOperand>",
		`out = strings.replaceAll(out, "\\5C", "_")`,
		`out = strings.replaceAll(out, "\\7B", "_")`,
		`out = strings.replaceAll(out, "\\7D", "_")`,
		`out = strings.replaceAll(out, "\\00", "_")`,
		`let lbrace = strings.replaceAll(newline, lirLBrace(), lirEsc("7B"))`,
		`let rbrace = strings.replaceAll(lbrace, lirRBrace(), lirEsc("7D"))`,
		"let noArgs: List<LirOperand> = []",
		"pub tempNames: List<String>",
		"let tempNames: List<String> = []",
		"l.tempNames.push(name)",
		`if !(loc.isParam) && typ.className == LirTypePtr`,
		`l.prologue.push(lirStore(lirOperand(typ, "null"), slot, 0))`,
		`value.typ.className == LirTypePtr && destType.className == LirTypeInt`,
		`value.typ.className == LirTypeInt && destType.className == LirTypePtr`,
		"value = lirCoerceValueToType(l, value, arg.typ, paramLocal.typ, paramType, \"direct call arg\")",
		"fn lirRuntimePanicDecl() -> LirRuntimeDecl",
		"lirRuntimeDeclWithAttrs(\"osty_rt_panic\", lirVoidType(), [lirPtrType()], false, lirRuntimePanicAttrs())",
		"if kind == MirIntrinsicAbort",
		"fn lirLowerMirAbort(l: LirMirFunctionLowerer, instr: MirInstr)",
		"l.instrs.push(lirCallWithAttrs(\"\", lirVoidType(), \"@osty_rt_panic\", args, \"\", lirRuntimePanicAttrs()))",
		"let fields: List<MirFieldLayout> = []",
		// MirStructLayout sentinel — pin the empty-name + size:0
		// prefix only. PR #1981 widened the struct with
		// `builtinSource` / `builtinSourceArgs`; pinning the full
		// field list would force this guard to churn every time the
		// struct grows a new field. The "empty layout" semantic that
		// `lirFindMirStructLayout` returns on miss is what matters.
		"MirStructLayout {name: \"\", mangled: \"\", fields, size: 0, align: 0,",
		"MirTupleLayout {key: \"\", mangled: \"\", fields}",
		"return lirLowerMirStringRuntimeCall(l, instr, llvmSetRuntimeNewSymbol(), lirPtrType(), lirNoTypeParams(), \"set.new\")",
		"l.instrs.push(lirCall(listReg, lirPtrType(), \"@\" + newSym, lirNoOperands()))",
		"fn lirLowerMirStdOsExecResultCall(l: LirMirFunctionLowerer, instr: MirInstr)",
		"symbol == \"osty_rt_os_exec_with\" || symbol == \"osty_rt_os_exec_input_with\"",
		"lirRuntimeDeclsDeclare(l.out.runtimeDecls, lirRuntimeDecl(instr.calleeSymbol, lirPtrType(), paramTypes, false))",
		"let boxType = lirRawType(lirBraced(\"i64, ptr, ptr\"))",
		"l.instrs.push(lirSelect(payloadI64, lirIntType(64), isOk, okPayloadI64, errPayloadI64))",
		"let comma = lirFirstTopLevelComma(args)",
		"strings.trim(strings.slice(args, 0, comma))",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("lir_proto.osty missing stage2-seed LIR Proto guard %q", needle)
		}
	}
	if strings.Contains(text, "strings.fromChar(ch)") {
		t.Fatalf("lir_proto.osty reintroduced stage2-unsafe strings.fromChar(ch) in generic arg splitting")
	}
}

func TestStage2SeedDriverAvoidsBootstrapUnsafeMatchDispatch(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	cases := []struct {
		path      string
		fn        string
		forbidden string
	}{
		{filepath.Join(root, "toolchain", "mir_validator.osty"), "fn mirValidateInstr", "match instr.kind"},
		{filepath.Join(root, "toolchain", "mir_validator.osty"), "fn mirValidateRValue", "match rv.kind"},
		{filepath.Join(root, "toolchain", "mir_validator.osty"), "fn mirValidateOperand", "match op.kind"},
		{filepath.Join(root, "toolchain", "mir_validator.osty"), "fn mirValidateProjection", "match proj.kind"},
		{filepath.Join(root, "toolchain", "mir_validator.osty"), "fn mirValidateTerm", "match t.kind"},
		{filepath.Join(root, "toolchain", "lir_proto.osty"), "pub fn lirRenderInstr", "match i.kind"},
	}
	for _, tc := range cases {
		src, err := os.ReadFile(tc.path)
		if err != nil {
			t.Fatalf("read %s: %v", tc.path, err)
		}
		text := string(src)
		start := strings.Index(text, tc.fn)
		if start < 0 {
			t.Fatalf("%s missing %s", tc.path, tc.fn)
		}
		end := strings.Index(text[start+1:], "\nfn ")
		pubEnd := strings.Index(text[start+1:], "\npub fn ")
		if end < 0 || (pubEnd >= 0 && pubEnd < end) {
			end = pubEnd
		}
		if end < 0 {
			t.Fatalf("%s body boundary missing for %s", tc.path, tc.fn)
		}
		body := text[start : start+1+end]
		if strings.Contains(body, tc.forbidden) {
			t.Fatalf("%s reintroduced bootstrap-unsafe match dispatch %q in %s", tc.path, tc.forbidden, tc.fn)
		}
	}
}

func TestSelfhostMirSupportIncludesListSetRuntimeSymbols(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(root, "scripts", "selfhost_mir_support.osty"))
	if err != nil {
		t.Fatalf("read selfhost_mir_support.osty: %v", err)
	}
	text := string(src)
	for _, needle := range []string{
		"pub fn llvmListRuntimeSetSymbol(suffix: String) -> String",
		`"osty_rt_list_set_" + suffix`,
		"pub fn llvmListRuntimeSetSymbolFor(elemTyp: String, isString: Bool) -> String",
		`return "osty_rt_list_set_string"`,
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("selfhost_mir_support.osty missing list-set runtime symbol helper %q", needle)
		}
	}
}

func TestMirValidatorReturnMismatchDiagnosticIsBootstrapSafe(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(root, "toolchain", "mir_validator.osty"))
	if err != nil {
		t.Fatalf("read mir_validator.osty: %v", err)
	}
	text := string(src)
	if strings.Contains(text, `type {ret.typ} does not match declared return type {func.returnType}`) {
		t.Fatalf("return mismatch diagnostic still uses bootstrap-unsafe interpolation placeholders")
	}
	if !strings.Contains(text, `" type " + ret.typ + " does not match declared return type " + func.returnType`) {
		t.Fatalf("return mismatch diagnostic should concatenate concrete values")
	}
}

func TestMirValidatorHotContextStringsAvoidInterpolation(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(root, "toolchain", "mir_validator.osty"))
	if err != nil {
		t.Fatalf("read mir_validator.osty: %v", err)
	}
	text := string(src)
	for _, needle := range []string{
		`mirValidateIndexCtx("aggregateRV.fields[", fi, "]")`,
		`mirValidateIndexCtx("call.args[", ai, "]")`,
		`mirValidateIndexCtx("intrinsic.args[", ai, "]")`,
		`mirValidateIndexedChildCtx(ctx, "projections", pi)`,
		`mirValidateChildCtx(ctx, "place")`,
		`prefix + mirIntToString(idx) + suffix`,
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("mir_validator.osty missing bootstrap-safe context helper use %q", needle)
		}
	}
	for _, forbidden := range []string{
		`"aggregateRV.fields[{fi}]"`,
		`"call.args[{ai}]"`,
		`"intrinsic.args[{ai}]"`,
		`"{ctx}.place"`,
		`"{ctx}.projections[{pi}]"`,
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("mir_validator.osty reintroduced hot-path interpolation %q", forbidden)
		}
	}
}

func TestVerifySelfRebuildStage1IgnoresStaleInTreeSelfBinary(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(root, "scripts", "verify-self-rebuild"))
	if err != nil {
		t.Fatalf("read verify-self-rebuild: %v", err)
	}
	text := string(src)
	// History: PR #1935 swapped the implicit `OSTY_STAGE0_FALLBACK=1`
	// env-var signal for an explicit `--bootstrap-stage0` CLI flag.
	// The flag was retired again (post-PR #1989) in favour of the
	// env-var gate, so install-self and the forked `osty build` now
	// share a single source of truth. The stage1 recovery path keeps
	// `OSTY_SELF_REGISTRY_OFFLINE=1` + `OSTY_STAGE0_LIST_ALL_DECLINES=1`
	// + `OSTY_STAGE0_FALLBACK=1` as the env-var trio. The
	// `--bootstrap-stage0` CLI flag MUST NOT reappear — its dual-parse
	// drift is what motivated the second swap.
	for _, needle := range []string{
		`rm -f "$toolchain_dir/.osty/out/debug/llvm/osty-self"`,
		`rm -f "$toolchain_dir/.osty/out/release/llvm/osty-self"`,
		"OSTY_SELF_REGISTRY_OFFLINE=1 OSTY_STAGE0_LIST_ALL_DECLINES=1 OSTY_STAGE0_FALLBACK=1",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("verify-self-rebuild missing stage1 stale self-binary guard %q", needle)
		}
	}
	if strings.Contains(text, "--bootstrap-stage0") {
		t.Fatalf("verify-self-rebuild reintroduced `--bootstrap-stage0` CLI flag; the env-var gate is the single source of truth post-retirement")
	}
}

func TestVerifySelfRebuildNormalizesMachORandomLinkMetadata(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(root, "scripts", "verify-self-rebuild"))
	if err != nil {
		t.Fatalf("read verify-self-rebuild: %v", err)
	}
	text := string(src)
	for _, needle := range []string{
		"normalized_sha256_file()",
		"LC_UUID = 0x1B",
		"LC_CODE_SIGNATURE = 0x1D",
		`data[off + 8:off + 24] = b"\0" * 16`,
		`data[dataoff:dataoff + datasize] = b"\0" * datasize`,
		"byte parity OK (Mach-O UUID/signature normalized)",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("verify-self-rebuild missing Mach-O parity normalization guard %q", needle)
		}
	}
}

func TestJustVerifySelfRebuildUsesCachedStage1Seed(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(root, "justfile"))
	if err != nil {
		t.Fatalf("read justfile: %v", err)
	}
	text := string(src)
	if !strings.Contains(text, "verify-self-rebuild: build-all\n    bash scripts/verify-self-rebuild --reuse-stage1 {{bin}}") {
		t.Fatalf("just verify-self-rebuild should use the cached stage1 seed, matching the production selfhost path")
	}
}

func TestMirLowerVariantPayloadZeroSelectorIsRedundant(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(root, "toolchain", "mir_lower.osty"))
	if err != nil {
		t.Fatalf("read mir_lower.osty: %v", err)
	}
	text := string(src)
	start := strings.Index(text, "fn mirLowerVariantNIsRedundantPayload")
	if start < 0 {
		t.Fatalf("mirLowerVariantNIsRedundantPayload missing")
	}
	end := strings.Index(text[start+1:], "\nfn ")
	if end < 0 {
		t.Fatalf("mirLowerVariantNIsRedundantPayload body boundary missing")
	}
	body := text[start : start+1+end]
	if !strings.Contains(body, "idx == 0") {
		t.Fatalf("variant payload selector 0 must stay redundant for nested enum payloads")
	}
	for _, forbidden := range []string{
		`namedName == "Option"`,
		`mirLowerTypeIsKnownEnum(l, t.namedName)`,
		"HirTypeOptional -> false",
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("variant payload selector 0 reintroduced nested algebraic exception %q", forbidden)
		}
	}
}
