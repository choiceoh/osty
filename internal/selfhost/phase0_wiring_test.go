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
// source probe so the status cannot drift back to a text-only Phase 0
// claim, or to a MIR-JSON-only backend pass, while the real source ->
// LLVM IR path regresses underneath it.
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
		"LLVM IR renderer produced no return instruction",
		"fn add(a: Int, b: Int) -> Int",
		"return add(x, 1)",
		"selfRebuildBundleSource",
		"selfRebuildRunClang",
		"selfRebuildWriteStringViaShell",
		`os.execWith("/bin/sh", ["-c", "cat > \"$1\"", "osty-self-write", path], contents`,
		"fn selfRebuildHostIsWindows() -> Bool {\n    false\n}",
		"selfRebuildRawSubcommand",
		`childEnv.insert("OSTY_SELF_REBUILD_FORWARD_ARGS", "")`,
		"let probeError = selfhostProbeError()",
		"osty-self source compiler: enabled",
		"osty-self self-rebuild probe: OK",
		"osty-self self-rebuild driver: source bundle -> clang object/runtime link",
	} {
		if !strings.Contains(text, needle) {
			t.Errorf("doctor probe missing %q", needle)
		}
	}
	for _, forbidden := range []string{
		"osty-self MIR JSON backend: enabled",
		"osty-self self-rebuild driver: MIR JSON backend -> clang object/runtime link",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("doctor regressed to MIR-JSON backend success marker %q", forbidden)
		}
	}
}

func TestMirLowerMethodBodyUsesOwnerForSyntheticSelf(t *testing.T) {
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
		`mirLowerFunctionBody(l, out, decl, retT, owner, asMethod, span)`,
		`pub fn mirLowerFunctionBody(l: MirLowerer, out: MirFunction, decl: HirFnDecl, retT: HirType, owner: String, asMethod: Bool, span: MirSpan)`,
		`fn mirLowerMethodParamsWithSelf(owner: String, decl: HirFnDecl) -> List<HirParam>`,
		`mirLowerMethodSignature(s.name, m.name, mirLowerMethodParamsWithSelf(s.name, m), m.ret, m.span)`,
		`mirLowerMethodSignature(e.name, m.name, mirLowerMethodParamsWithSelf(e.name, m), m.ret, m.span)`,
		`let selfOwner = if owner != ""`,
		`hirParam("self", hirTypeNamed(selfOwner, []), decl.span)`,
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("mir_lower.osty missing synthetic self owner guard %q", needle)
		}
	}
	if strings.Contains(text, `hirParam("self", hirTypeNamed(mirLowerSelfOwnerName(decl), []), decl.span)`) {
		t.Fatalf("method body lowering reintroduced decl-param owner recovery for synthetic self")
	}
}

func TestMirLowerLookupsUseIndexMaps(t *testing.T) {
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
		"pub fnSigIndex: Map<String, Int>",
		"pub methodSigIndex: Map<String, Int>",
		"pub structIndex: Map<String, Int>",
		"pub enumIndex: Map<String, Int>",
		"pub globalIndex: Map<String, Int>",
		"pub useAliasIndex: Map<String, Int>",
		"l.fnSigIndex.insert(decl.name, idx)",
		"l.methodSigIndex.insert(key, methodIdx)",
		"l.structIndex.insert(s.name, structIdx)",
		"l.enumIndex.insert(e.name, enumIdx)",
		"l.globalIndex.insert(g.name, globalIdx)",
		"l.useAliasIndex.insert(u.alias, aliasIdx)",
		"match l.fnSigIndex.get(name)",
		"match l.methodSigIndex.get(key)",
		"if mirLowerEnumForVariant(bs.l, e.identName) != \"\" {",
		"return mirLowerIdentVariantOperand(bs, e)",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("mir_lower.osty missing lookup-index guard %q", needle)
		}
	}
}

func TestMonomorphTypeCloneAvoidsDeadPayloadAllocations(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(root, "toolchain", "monomorph_pass.osty"))
	if err != nil {
		t.Fatalf("read monomorph_pass.osty: %v", err)
	}
	text := string(src)
	start := strings.Index(text, "pub fn hirCloneType(t: HirType) -> HirType {")
	if start < 0 {
		t.Fatalf("monomorph_pass.osty missing hirCloneType")
	}
	endMarker := "/// hirCloneTypeList applies hirCloneType"
	end := strings.Index(text[start:], endMarker)
	if end < 0 {
		t.Fatalf("monomorph_pass.osty missing hirCloneTypeList marker")
	}
	cloneBody := text[start : start+end]
	for _, needle := range []string{
		"if t.kind == HirTypeNamed",
		"let mut out = t",
		"out.namedArgs = hirCloneTypeList(t.namedArgs)",
		"if t.kind == HirTypeFn",
		"out.fnParams = hirCloneTypeList(t.fnParams)",
		"out.fnReturn = hirCloneTypeList(t.fnReturn)",
		"\n    t\n}",
	} {
		if !strings.Contains(cloneBody, needle) {
			t.Fatalf("hirCloneType missing allocation-tight variant clone %q", needle)
		}
	}
	for _, forbidden := range []string{
		"let mut out = hirTypeInvalid()",
		"HirType {kind:",
	} {
		if strings.Contains(cloneBody, forbidden) {
			t.Fatalf("hirCloneType reintroduced dead payload allocation path %q", forbidden)
		}
	}

	if strings.Contains(text, "let mut out = hirCloneType(t)\n        out.namedArgs = hirSubstTypeList(t.namedArgs, env)") {
		t.Fatalf("hirSubstType reintroduced clone-then-overwrite for named type args")
	}
	if !strings.Contains(text, "let idx = hirSubstEnvIndex(env, t.varName)") {
		t.Fatalf("hirSubstType should avoid double env lookup for type variables")
	}
	if !strings.Contains(text, "out.namedArgs = hirSubstTypeList(t.namedArgs, env)") {
		t.Fatalf("hirSubstType should substitute named args without allocating dead payload lists")
	}
}

func TestElabTypedNodeLookupUsesIndexCache(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(root, "toolchain", "elab.osty"))
	if err != nil {
		t.Fatalf("read elab.osty: %v", err)
	}
	text := string(src)
	for _, needle := range []string{
		"pub typedExprCoreByNode: List<Int>",
		"pub typedExprTypeByNode: List<Int>",
		"pub typedStmtCoreByNode: List<Int>",
		"elabEnsureIntListLen(cx.typedExprCoreByNode, idx + 1)",
		"cx.typedExprCoreByNode[idx] = out.node",
		"cx.typedExprTypeByNode[idx] = out.ty",
		"elabEnsureIntListLen(cx.typedStmtCoreByNode, idx + 1)",
		"cx.typedStmtCoreByNode[idx] = coreIdx",
		"fn elabEnsureIntListLen(items: List<Int>, targetLen: Int)",
		"if idx >= 0 && idx < cx.typedExprCoreByNode.len()",
		"if idx >= 0 && idx < cx.typedExprTypeByNode.len()",
		"if idx >= 0 && idx < cx.typedStmtCoreByNode.len()",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("elab.osty missing typed-node lookup cache guard %q", needle)
		}
	}
}

func TestHirCloneOnlyCopiesLiveVariantPayloads(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(root, "toolchain", "hir_clone.osty"))
	if err != nil {
		t.Fatalf("read hir_clone.osty: %v", err)
	}
	text := string(src)
	for _, needle := range []string{
		"pub fn hirCloneExpr(e: HirExpr) -> HirExpr {\n    let mut out = e",
		"if e.kind == HirExprBinary",
		"out.binaryLeft = hirCloneExprList(e.binaryLeft)",
		"out.callArgs = hirCloneArgList(e.callArgs)",
		"if e.matchExprHasTree",
		"pub fn hirCloneStmt(stmt: HirStmt) -> HirStmt {\n    let mut out = stmt",
		"if stmt.kind == HirStmtLet",
		"if stmt.matchHasTree",
		"pub fn hirClonePattern(p: HirPattern) -> HirPattern {\n    let mut out = p",
		"if p.kind == HirPatBinding",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("hir_clone.osty missing live-payload clone guard %q", needle)
		}
	}
	for _, forbidden := range []string{
		"HirExpr {kind: e.kind",
		"HirStmt {kind: stmt.kind",
		"HirPattern {kind: p.kind",
		"HirDecisionNode {kind: n.kind",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("hir_clone.osty reintroduced full-payload clone %q", forbidden)
		}
	}
}

func TestMirLowerBooleanShortCircuitUsesCFG(t *testing.T) {
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
		"if e.kind == HirExprIf {",
		"if e.kind == HirExprBinary {",
		"fn mirLowerBoolShortCircuitInto(bs: MirLowerBodyState, e: HirExpr, dest: MirPlace, destT: HirType) -> Bool",
		"if e.binaryOp != HirBinAnd && e.binaryOp != HirBinOr",
		"mirLowerTerminateBranch(bs, left, rightBB, constBB, span)",
		"mirLowerAssignBoolConst(bs, dest, false, span)",
		"mirLowerTerminateBranch(bs, left, constBB, rightBB, span)",
		"mirLowerAssignBoolConst(bs, dest, true, span)",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("mir_lower.osty missing short-circuit CFG guard %q", needle)
		}
	}
}

func TestLirMirMutableBlockAbiPassesBasicBlockByRef(t *testing.T) {
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
		`name == "HirBlock"`,
		`name == "MirBasicBlock"`,
		`fn lirMirParamShouldPassByRef(typeName: String, typ: LirType) -> Bool`,
		`fn lirLowerMirAggregateArgAddress(l: LirMirFunctionLowerer, arg: MirOperand, paramTypeName: String, paramType: LirType, context: String) -> LirOperand`,
		`let value = lirLowerMirOperand(l, l.instrs, arg, paramTypeName, paramType)`,
		`l.instrs.push(lirAlloca(tmp, value.typ, 0))`,
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("lir_proto.osty missing mutable MIR block ABI guard %q", needle)
		}
	}
	if strings.Contains(text, "if address.value != \"\" {\n            return address\n        }\n        return lirOperandInvalid()\n    }\n    let value = lirLowerMirOperand") {
		t.Fatalf("lirLowerMirAggregateArgAddress still drops non-addressable aggregate args instead of spilling")
	}
	mirSrc, err := os.ReadFile(filepath.Join(root, "toolchain", "mir.osty"))
	if err != nil {
		t.Fatalf("read mir.osty: %v", err)
	}
	mirText := string(mirSrc)
	for _, needle := range []string{
		`func.blocks[blockId].term.kind = term.kind`,
		`func.blocks[blockId].term.kind = MirTermGoto`,
		`func.blocks[blockId].term.kind = MirTermBranch`,
		`func.blocks[blockId].term.kind = MirTermSwitchInt`,
		`func.blocks[blockId].hasTerm = true`,
	} {
		if !strings.Contains(mirText, needle) {
			t.Fatalf("mir.osty missing direct block terminator write guard %q", needle)
		}
	}
}

func TestLirBareFnThunkDedupUsesMap(t *testing.T) {
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
		"pub thunkSymbolSet: Map<String, Bool>",
		"let thunkSymbolSet: Map<String, Bool> = {:}",
		"match l.out.thunkSymbolSet.get(symbol)",
		"l.out.thunkSymbolSet.insert(symbol, true)",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("lir_proto.osty missing bare-fn thunk dedup map guard %q", needle)
		}
	}
	if strings.Contains(text, "if listContainsString(l.out.thunkSymbols, symbol)") {
		t.Fatalf("lirEnsureBareFnThunk reintroduced linear thunk-symbol scan")
	}
}

func TestLirMirTypeLoweringCachesRecursiveLayouts(t *testing.T) {
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
		"pub typeInProgress: Map<String, Bool>",
		"pub mirTypeCache: Map<String, LirType>",
		"let typeInProgress: Map<String, Bool> = {:}",
		"let mirTypeCache: Map<String, LirType> = {:}",
		"typeInProgress: l.typeInProgress",
		"l.out.mirTypeCache.insert(name, typ)",
		"match l.out.mirTypeCache.get(name)",
		"fn lirLowerMirTypeCacheAliases(",
		"fn lirLowerMirTypeSetProgress(",
		"fn lirLowerMirTypeInProgress(",
		"if lirLowerMirTypeInProgress(l, name, layout.name, layout.mangled)",
		"fn lirDeclareMirLayoutTypeDefs(out: LirModule, mir: MirModule, diags: List<LirDiagnostic>)",
		"lirDeclareMirLayoutTypeDefs(out, mir, diags)",
		"fn lirMirStructLayoutBodyShallow(",
		"fn lirLowerMirType_module_shallow(",
		"let body = lirMirStructLayoutBodyShallow(l.out, l.mirModule, layout",
		"lirModuleDeclareTypeDef(l.out, lirTypeDef(typeName, body))",
		"return lirLowerMirTypeCacheAliases(l, name, layout.name, layout.mangled, aggregateType)",
		"if lirLowerMirTypeInProgress(l, name, layout.key, layout.mangled)",
		"return lirLowerMirTypeCacheAliases(l, name, layout.key, layout.mangled, aggregateType)",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("lir_proto.osty missing recursive type-cache guard %q", needle)
		}
	}
}

func TestLirMirLayoutLookupUsesIndexMaps(t *testing.T) {
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
		"pub struct LirMirLayoutIndex",
		"pub layoutIndex: LirMirLayoutIndex",
		"fn lirMirBuildLayoutIndex(layouts: MirLayoutTable) -> LirMirLayoutIndex",
		"lirMirLayoutIndexPutQualified(index.structs, layout.name, i)",
		"for i < parts.len()",
		"suffix = suffix + \".\" + parts[j]",
		"lirMirLayoutIndexPut(index.tuples, layout.key, i)",
		"fn lirMirLayoutIndexStructPos(index: LirMirLayoutIndex, name: String) -> Int",
		"out.layoutIndex = lirMirBuildLayoutIndex(mir.layouts)",
		"out.layoutIndex = lirMirBuildLayoutIndex(layouts)",
		"let structPos = lirMirLayoutIndexStructPos(l.out.layoutIndex, name)",
		"let tuplePos = lirMirLayoutIndexTuplePos(l.out.layoutIndex, name)",
		"let enumPos = lirMirLayoutIndexEnumPos(l.out.layoutIndex, name)",
		"let ifacePos = lirMirLayoutIndexInterfacePos(l.out.layoutIndex, name)",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("lir_proto.osty missing layout index guard %q", needle)
		}
	}
}

func TestSelfResolverScopesAvoidCopyingRootSymbols(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(root, "toolchain", "resolve.osty"))
	if err != nil {
		t.Fatalf("read resolve.osty: %v", err)
	}
	text := string(src)
	for _, needle := range []string{
		"rootIndex: Map<String, Int>",
		"locals: List<SelfSymbol>",
		"rootIndex: srBuildSymbolIndex(result.symbols)",
		"locals: srSymbolListCopy(parent.locals)",
		"fn srBuildSymbolIndex(symbols: List<SelfSymbol>) -> Map<String, Int>",
		"match scope.rootIndex.get(name)",
		"srSymbolExistsAtDepth(scope.locals, name, depth)",
		"let bound = srSymbolsAtDepth(altScope.locals, altScope.depth)",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("resolve.osty missing indexed scope guard %q", needle)
		}
	}
	for _, forbidden := range []string{
		"symbols: srSymbolListCopy(result.symbols)",
		"srSymbolListCopy(parent.symbols)",
		"for sym in scope.symbols",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("resolve.osty reintroduced full root-scope copying %q", forbidden)
		}
	}
}

func TestLirRenderedFunctionTextUsesLineJoin(t *testing.T) {
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
		"fn lirRenderFunctionTextBlocks(blocks: List<String>, pos: Int, lines: List<String>) -> List<String>",
		"lines = lirRenderFunctionTextBlocks(blocks, 0, lines)",
		"strings.join(lines, lirLF())",
		"fn lirRenderInstrsText(instrs: List<LirInstr>, pos: Int, lines: List<String>) -> List<String>",
		"lines.push(\"  \" + text)",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("lir_proto.osty missing line-join render guard %q", needle)
		}
	}
	for _, forbidden := range []string{
		"out + lirLF() + block",
		"out + lirLF() + \"  \" + text",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("lir_proto.osty reintroduced quadratic render concat %q", forbidden)
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
	if strings.Contains(text[guardPos:], "match node.kind") {
		t.Fatalf("elabInferImpl reintroduced node.kind match after loopexpr guard")
	}
	ifPos := strings.Index(text[guardPos:], "if node.kind == AstNIntLit")
	if ifPos < 0 {
		t.Fatalf("elabInferImpl missing bootstrap-safe if-chain after loopexpr guard")
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
		if strings.Contains(text, `fn main() -> Int \{\n`) {
			t.Fatalf("%s reintroduced escaped probe source; self-host string escaping writes literal backslashes", path)
		}
		if !strings.Contains(text, "r\"\"\"\n") {
			t.Fatalf("%s missing raw self-host doctor probe source", path)
		}
		if strings.HasSuffix(path, "main.osty") {
			if !strings.Contains(text, "fn add(a: Int, b: Int) -> Int") || !strings.Contains(text, "return add(x, 1)\n}\n\"\"\"") {
				t.Fatalf("%s missing raw function-call doctor probe source", path)
			}
		} else if !strings.Contains(text, "fn main() -> Int {") || !strings.Contains(text, "return x + 1\n}\n\"\"\"") {
			t.Fatalf("%s missing raw closing brace in self-host doctor probe source", path)
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
	hirSrc, err := os.ReadFile(filepath.Join(root, "toolchain", "hir_lower.osty"))
	if err != nil {
		t.Fatalf("read hir_lower.osty: %v", err)
	}
	hirText := string(hirSrc)
	for _, needle := range []string{
		"let mut body = if node.right >= 0",
		"if node.right >= 0 && body.stmts.len() == 0 && !body.hasResult",
		"hirBlockAppend(body, hirExprStmt(hirUnitLit(span), span))",
		"fn hirLowerDecodeStringEscapes(body: String) -> String",
		"ostyDecodeEscapes(body)",
		"if node.kind != AstNType {",
		"return hirTypeNamed(node.text, [])",
	} {
		if !strings.Contains(hirText, needle) {
			t.Fatalf("hir_lower.osty missing explicit-empty-body guard %q", needle)
		}
	}
	if strings.Contains(hirText, `out = out + next.toString()`) || strings.Contains(hirText, `.toChar().toString()`) {
		t.Fatalf("hir_lower.osty string escape decoding reintroduced bootstrap-unsafe Char.toString lowering")
	}
	for _, forbidden := range []string{
		"let primHit = match prim.kind",
		"let isPrim = match prim.kind",
		"let isIntLit = match e.kind",
		"let isStringLit = match e.kind",
		"let isIf = match e.kind",
		"let isMatch = match e.kind",
	} {
		if strings.Contains(hirText, forbidden) {
			t.Fatalf("hir_lower.osty reintroduced bootstrap-unsafe match predicate %q", forbidden)
		}
	}
	lexerSrc, err := os.ReadFile(filepath.Join(root, "toolchain", "lexer.osty"))
	if err != nil {
		t.Fatalf("read lexer.osty: %v", err)
	}
	lexerText := string(lexerSrc)
	for _, needle := range []string{
		"fn ostyDecodeCodepointString(value: Int) -> String",
		`out = out + ostyDecodeCodepointString(hi * 16 + lo)`,
		`out = out + ostyDecodeCodepointString(scan.value)`,
	} {
		if !strings.Contains(lexerText, needle) {
			t.Fatalf("lexer.osty escape decoder missing bootstrap-safe codepoint path %q", needle)
		}
	}
	if strings.Contains(lexerText, `bytes.toString(bytes.from(buf))`) || strings.Contains(lexerText, `bytes.toString(bytes.from(buf)).unwrapOr("")`) {
		t.Fatalf("lexer.osty escape decoder reintroduced bootstrap-unsafe bytes.toString lowering")
	}
	hirCoreSrc, err := os.ReadFile(filepath.Join(root, "toolchain", "hir.osty"))
	if err != nil {
		t.Fatalf("read hir.osty: %v", err)
	}
	hirCoreText := string(hirCoreSrc)
	for _, needle := range []string{
		"e.binaryLeft = [left]",
		"e.binaryRight = [right]",
		"e.callCallee = [callee]",
		"t.optionalInner = [inner]",
		"t.fnReturn = [ret]",
		"if t.kind == HirTypePrim {",
		"if t.primKind == HirPrimUnit {",
		"if t.primKind == HirPrimNever {",
	} {
		if !strings.Contains(hirCoreText, needle) {
			t.Fatalf("hir.osty singleton constructor missing direct list assignment %q", needle)
		}
	}
	for _, forbidden := range []string{
		"e.binaryLeft.push(left)",
		"e.binaryRight.push(right)",
		"e.callCallee.push(callee)",
		"t.optionalInner.push(inner)",
		"t.fnReturn.push(ret)",
		"pub fn hirTypeIsUnit(t: HirType) -> Bool {\n    match t.kind",
		"pub fn hirTypeIsNever(t: HirType) -> Bool {\n    match t.kind",
	} {
		if strings.Contains(hirCoreText, forbidden) {
			t.Fatalf("hir.osty reintroduced bootstrap-unsafe singleton field push %q", forbidden)
		}
	}
	elabSrc, err := os.ReadFile(filepath.Join(root, "toolchain", "elab.osty"))
	if err != nil {
		t.Fatalf("read elab.osty: %v", err)
	}
	elabText := string(elabSrc)
	for _, needle := range []string{
		"if node.kind == AstNIdent {",
		"return elabInferIdent(cx, node)",
		"if node.kind == AstNCall {",
		"return elabInferCall(cx, idx, node, tErr(cx.env.tys))",
		"if node.kind == AstNReturn {",
		"out = elabReturnStmt(cx, node)",
		"if node.kind == AstNIdent || node.kind == AstNParen || node.kind == AstNBlock || node.kind == AstNUnary || node.kind == AstNBinary {",
	} {
		if !strings.Contains(elabText, needle) {
			t.Fatalf("elab.osty missing bootstrap-safe node-kind dispatch guard %q", needle)
		}
	}
	for _, forbidden := range []string{
		"match node.kind {\n        AstNIntLit -> elabInferIntLit",
		"match node.kind {\n        AstNIntLit -> elabCheckIntLit",
		"let out = match node.kind {",
		"match node.kind {\n        AstNIntLit -> true,",
	} {
		if strings.Contains(elabText, forbidden) {
			t.Fatalf("elab.osty reintroduced bootstrap-unsafe node-kind match dispatch %q", forbidden)
		}
	}
	checkSrc, err := os.ReadFile(filepath.Join(root, "toolchain", "check.osty"))
	if err != nil {
		t.Fatalf("read check.osty: %v", err)
	}
	checkText := string(checkSrc)
	for _, needle := range []string{
		"for paramIdx in node.children {",
		"let paramNode = astArenaNodeAt(cx.ast.arena, paramIdx)",
		`if sig.receiverTy >= 0 && paramNode.text == "self" {`,
		`let rawName = if paramNode.text != "" {`,
		"checkBindSpan(cx.env, bindName, pty, true, paramNode.start, paramNode.end)",
	} {
		if !strings.Contains(checkText, needle) {
			t.Fatalf("check.osty missing AST-backed fn param binding guard %q", needle)
		}
	}
	if strings.Contains(checkText, "for pname in sig.paramNames {\n        let pty =") {
		t.Fatalf("check.osty reintroduced signature-only fn param binding")
	}
	src, err := os.ReadFile(filepath.Join(root, "toolchain", "mir_lower.osty"))
	if err != nil {
		t.Fatalf("read mir_lower.osty: %v", err)
	}
	text := string(src)
	for _, needle := range []string{
		"if s.kind == HirStmtLet {",
		"if s.kind == HirStmtReturn {",
		"if s.kind == HirStmtIf {",
		"if s.kind == HirStmtMatch {",
		"if s.exprValue.kind == HirExprCall",
		"mirLowerCallExprInto(bs, s.exprValue, mirPlace(-1), hirTUnit())",
		"if s.exprValue.kind == HirExprMethod",
		"mirLowerMethodCallInto(bs, s.exprValue, mirPlace(-1), hirTUnit())",
		"let mut localIdx = bs.fn_.locals.len() - 1",
		"if loc.name == name {",
		"if e.identKind == HirIdentFn {",
		"if e.identKind == HirIdentGlobal {",
		"if e.identKind == HirIdentVariant {",
		"if pat.kind == HirPatIdent {",
		"mirLowerBindPatternName(bs, pat.identName, pat.identMut, scrutinee, scrutT, span)",
		"if pat.kind == HirPatBinding {",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("mir_lower.osty missing statement-call discard guard %q", needle)
		}
	}
	if strings.Contains(text, "pub fn mirLowerStmt(bs: MirLowerBodyState, s: HirStmt) {\n    match s.kind") {
		t.Fatalf("mirLowerStmt reintroduced bootstrap-unsafe match dispatch")
	}
	if strings.Contains(text, "fn mirLowerIdentOperand(bs: MirLowerBodyState, e: HirExpr) -> MirOperand {\n") &&
		strings.Contains(text, "    match e.identKind {") {
		t.Fatalf("mirLowerIdentOperand reintroduced bootstrap-unsafe ident-kind match dispatch")
	}
	if strings.Contains(text, "fn mirLowerIdentPlace(bs: MirLowerBodyState, e: HirExpr) -> MirPlace {\n") &&
		strings.Contains(text, "    match e.identKind {") {
		t.Fatalf("mirLowerIdentPlace reintroduced bootstrap-unsafe ident-kind match dispatch")
	}
	if strings.Contains(text, "fn mirLowerBindPattern(bs: MirLowerBodyState, pat: HirPattern, scrutinee: MirPlace, scrutT: HirType, span: MirSpan) {\n") &&
		strings.Contains(text, "    match pat.kind {") {
		t.Fatalf("mirLowerBindPattern reintroduced bootstrap-unsafe pattern-kind match dispatch")
	}
	tySrc, err := os.ReadFile(filepath.Join(root, "toolchain", "ty.osty"))
	if err != nil {
		t.Fatalf("read ty.osty: %v", err)
	}
	tyText := string(tySrc)
	for _, needle := range []string{
		"if prim == PkInt {",
		"return arena.idxInt",
		"if na.kind == TkPrim {",
		"return na.prim == nb.prim",
		"if node.kind == TkPrim {",
		"return primKindName(node.prim)",
		"if node.kind == TkNamed {",
	} {
		if !strings.Contains(tyText, needle) {
			t.Fatalf("ty.osty missing bootstrap-safe type dispatch guard %q", needle)
		}
	}
	for _, forbidden := range []string{
		"match prim {",
		"match na.kind {",
		"match node.kind {",
	} {
		if strings.Contains(tyText, forbidden) {
			t.Fatalf("ty.osty reintroduced bootstrap-unsafe match dispatch %q", forbidden)
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
		"fn lirStoreIndexedListRegisterValue(l: LirMirFunctionLowerer, listReg: String, proj: MirProjection, elemName: String, value: LirOperand, valueMirType: String, context: String)",
		"lirStoreIndexedListRegisterValue(l, current.value, proj, proj.typ, stored, leafTypeName, context)",
		"listStoreActive = true",
		"lirStoreIndexedListRegisterValue(l, listStoreReg, listStoreProj, listStoreElemName, rebuilt, listStoreElemName, context)",
		"let narrowed = lirNarrowI64ToType(l, current.value, step.typ)",
		"fn lirLowerMirStdOsOutputFieldProjection(l: LirMirFunctionLowerer, instrs: List<LirInstr>, value: LirOperand, typeName: String, proj: MirProjection) -> LirOperand",
		"fn lirStdOsOutputFieldType(typeName: String, index: Int) -> LirType",
		`lirRawType(lirBraced("i64, ptr, ptr, i1"))`,
		"llvmListRuntimeSetSymbolFor(elemLane.llvm, lirContainerElemIsString(elemName))",
		"mirRtListSymbol(\"set_bytes_v1\")",
		"fn lirCoerceValueToType(l: LirMirFunctionLowerer, value: LirOperand, valueMirType: String, destTypeName: String, destType: LirType, context: String) -> LirOperand",
		"fn lirRebuildDefaultI64Aggregate(l: LirMirFunctionLowerer, value: LirOperand, destType: LirType) -> LirOperand",
		"fn lirRebuildAlgebraicAggregate(l: LirMirFunctionLowerer, value: LirOperand, destTypeName: String, destType: LirType) -> LirOperand",
		"fn lirRebuildDefaultI64AggregateIntoWidened(l: LirMirFunctionLowerer, value: LirOperand, destTypeName: String, destType: LirType) -> LirOperand",
		"lirUnboxCompositeFromI64(l, payloadI64, payloadType)",
		"value = LirOperand {typ: destType, value: lirZeroValue(destType)}",
		"fn lirMirStructLayoutDirectlyMentionsType(out: LirModule, mir: MirModule, structName: String, typeName: String) -> Bool",
		"lirMirStructLayoutDirectlyMentionsType(out, mir, payloadInner, name)",
		"for layout in mir.layouts.structs {\n        if lirMirStructLayoutMatches(layout, name) {",
		"for layout in mir.layouts.enums {\n        if lirMirEnumLayoutMatches(layout, name) {",
		"fn lirEmitEnumDiscriminantAggregate(l: LirMirFunctionLowerer, discValue: LirOperand, destType: LirType) -> LirOperand",
		"lirCoerceTargetIsPayloadEnum(l, destTypeName)",
		"let mut payloadI64 = \"0\"",
		"if lirTypeIsVoid(value.typ) || arg.typ == \"Unit\" || arg.typ == \"()\"",
		"} else if rv.aggFields.len() > 1 {",
		"fn lirAppendRenderedLirBlocks(renderedBlocks: List<String>, blocks: List<LirBlock>, pos: Int)",
		"lirAppendRenderedLirBlocks(renderedBlocks, blockLowerer.extraBlocks, 0)",
		"fn lirLowerMirListGetSafe(l: LirMirFunctionLowerer, instr: MirInstr, optTypeName: String)",
		"lirLowerMirListGetSafe(l, instr, effectiveTypeName)",
		"fn lirBinaryAddIsStringConcat(rv: MirRValue, hintName: String, hint: LirType) -> Bool",
		"if lirTypeIsZero(argType) {\n        if !(lirTypeIsZero(left.typ)) {",
		"left = lirCoerceValueToType(l, left, rv.arg.typ, rv.arg.typ, argType, \"binary left\")",
		"right = lirCoerceValueToType(l, right, rv.right.typ, rv.right.typ, argType, \"binary right\")",
		"left = lirCoerceValueToType(l, left, rv.arg.typ, \"String\", lirPtrType(), \"string concat left\")",
		"fn lirBinaryIsStringComparison(rv: MirRValue) -> Bool",
		"fn lirLowerMirStringComparison(l: LirMirFunctionLowerer, rv: MirRValue) -> LirOperand",
		"lirRuntimeDeclI1FromTwoPtr(symbol)",
		"lirRuntimeDeclI64FromTwoPtr(symbol)",
		"fn lirMirParamAbiType(typeName: String, typ: LirType) -> LirType",
		"fn lirMirParamShouldPassByRef(typeName: String, typ: LirType) -> Bool",
		"fn lirMirParamNameShouldPassByRef(name: String) -> Bool",
		`name == "OstyParser"`,
		`name == "MirLowerBodyState"`,
		`name == "LirMirFunctionLowerer"`,
		"if lirMirParamShouldPassByRef(typeName, typ) {\n        return lirPtrType()",
		"let abiType = lirMirParamAbiType(loc.typ, typ)",
		"if loc.isParam && lirMirParamShouldPassByRef(loc.typ, typ)",
		"slot: lirMirParamName(loc.id), typ",
		"fn lirLowerMirAggregateArgAddress(l: LirMirFunctionLowerer, arg: MirOperand, paramTypeName: String, paramType: LirType, context: String) -> LirOperand",
		"fn lirLowerMirAggregatePlaceAddress(l: LirMirFunctionLowerer, place: MirPlace, rootTypeName: String, context: String) -> LirOperand",
		"l.instrs.push(lirGep(nextPtr, currentType, currentPtr",
		"lirMirParamShouldPassByRef(paramLocal.typ, paramValueType) && paramType.className == LirTypePtr",
		"let valuePtr = lirLowerMirAggregateArgAddress(l, arg, paramLocal.typ, paramValueType, \"direct call arg\")",
		"l.instrs.push(lirCall(eqDest, boolType, \"@\" + symbol",
		"l.instrs.push(lirBinary(neDest, \"xor\", boolType, eqDest, \"1\"))",
		"out = strings.replaceAll(out, \"<\", \".\")",
		"out = strings.replaceAll(out, \" \", \"\")",
		"out = strings.replaceAll(out, \"()\", \"Unit\")",
		"out = strings.replaceAll(out, \"(\", \".tuple.\")",
		"out = strings.replaceAll(out, \"[\", \".array.\")",
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
		`if lirTypeNameIsGCManaged(loc.typ) && !(loc.isParam) && typ.className == LirTypePtr`,
		`l.prologue.push(lirStore(lirOperand(typ, "null"), slot, 0))`,
		`if lirTypeNameIsGCManaged(loc.typ) && loc.isParam`,
		`value.typ.className == LirTypePtr && destType.className == LirTypeInt`,
		`value.typ.className == LirTypeInt && destType.className == LirTypePtr`,
		"value = lirCoerceValueToType(l, value, arg.typ, paramLocal.typ, paramType, \"direct call arg\")",
		"value = lirCoerceValueToType(l, value, field.typ, fieldLayout.typ, fieldType, \"aggregate.tuple\")",
		"value = lirCoerceValueToType(l, value, field.typ, fieldLayout.typ, fieldType, \"aggregate.struct\")",
		"fn lirRuntimePanicDecl() -> LirRuntimeDecl",
		"lirRuntimeDeclWithAttrs(\"osty_rt_panic\", lirVoidType(), [lirPtrType()], false, lirRuntimePanicAttrs())",
		"if kind == MirIntrinsicAbort",
		"return \"define \" + fn_.linkage",
		"fn lirEmitGcRootReleases(l: LirMirFunctionLowerer)",
		"if l.gcRootSlots.len() == 0",
		"lirRuntimeDeclsDeclare(l.out.runtimeDecls, lirGcRootReleaseDecl())",
		"let mut i = l.gcRootSlots.len()",
		"l.instrs.push(lirCall(\"\", lirVoidType(), \"@\" + lirGcRootReleaseSymbol(), [lirOperand(lirPtrType(), slot)]))",
		"fn lirLowerMirAbort(l: LirMirFunctionLowerer, instr: MirInstr)",
		"l.instrs.push(lirCallWithAttrs(\"\", lirVoidType(), \"@osty_rt_panic\", args, \"\", lirRuntimePanicAttrs()))",
		"fn lirIsTupleTypeName(name: String) -> Bool",
		"fn lirLowerMirSyntheticTupleType(l: LirMirFunctionLowerer, name: String) -> LirType",
		"fn lirSyntheticTupleLayout(name: String) -> MirTupleLayout",
		"if valueReg == \"\" || lirTypeIsVoid(valueType) {\n        return \"0\"",
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
		"out.renderedFunctions.push(lirRenderFunction(ctor))",
		"out.renderedFunctions.push(lirRenderFunction(fn_))",
		"let externRet = if lirIsStdFsRuntimeResultSymbol(name) {",
		"fn lirIsStdFsRuntimeResultSymbol(symbol: String) -> Bool",
		`symbol == "std.fs.readToString" || symbol == "std.fs.walk" || symbol == "std.fs.mkdirAll" || symbol == "std.fs.writeString"`,
		"fn lirLowerMirRuntimeBoxedResultCall(l: LirMirFunctionLowerer, instr: MirInstr, context: String)",
		"lirLowerMirRuntimeBoxedResultCall(l, instr, \"call.std.fs.result\")",
		"fn lirLowerMirStdOsExecResultCall(l: LirMirFunctionLowerer, instr: MirInstr)",
		"symbol == \"osty_rt_os_exec_with\" || symbol == \"osty_rt_os_exec_input_with\"",
		"lirRuntimeDeclsDeclare(l.out.runtimeDecls, lirRuntimeDecl(instr.calleeSymbol, lirPtrType(), paramTypes, false))",
		"l.instrs.push(lirLoad(loaded, resultType, boxed, 8))",
		"lirEmitContainerCall(l, instr, llvmMapRuntimeRemoveSymbol(recv.elemLir.llvm, recv.isString), lirIntType(1)",
		"let boxType = lirRawType(lirBraced(\"i64, ptr, ptr\"))",
		"l.instrs.push(lirSelect(payloadI64, lirIntType(64), isOk, okPayloadI64, errPayloadI64))",
		"if strings.slice(typeName, 0, prefix.len()) != prefix",
		`if strings.slice(typeName, typeName.len() - 1, typeName.len()) != ">"`,
		"strings.slice(typeName, prefix.len(), typeName.len() - 1)",
		"let comma = lirFirstTopLevelComma(args)",
		"let ch = strings.slice(s, i, i + 1)",
		"strings.trim(strings.slice(args, 0, comma))",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("lir_proto.osty missing stage2-seed LIR Proto guard %q", needle)
		}
	}
	if strings.Contains(text, "strings.fromChar(ch)") {
		t.Fatalf("lir_proto.osty reintroduced stage2-unsafe strings.fromChar(ch) in generic arg splitting")
	}
	if strings.Contains(text, `strings.trimPrefix(typeName, prefix)`) || strings.Contains(text, `strings.trimSuffix(inner, ">")`) {
		t.Fatalf("lir_proto.osty reintroduced helper-based generic arg slicing in lirContainerInnerArg")
	}
	if strings.Contains(text, "let typeChars = strings.chars(typeName)") ||
		strings.Contains(text, "let prefixChars = strings.chars(prefix)") ||
		strings.Contains(text, "let ch = chars[i]") {
		t.Fatalf("lir_proto.osty reintroduced stage2-unsafe Char generic parsing")
	}
}

func TestAirepairPythonScopeKindsAvoidGlobalRefInReturnStructs(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(root, "toolchain", "airepair_python.osty"))
	if err != nil {
		t.Fatalf("read airepair_python.osty: %v", err)
	}
	text := string(src)
	for _, needle := range []string{
		"pub let pythonScopeBlock: Int = 0",
		"pub let pythonScopeMatch: Int = 1",
		"pub let pythonScopeMatchArm: Int = 2",
		"fn pythonScopeBlockCode() -> Int",
		"fn pythonScopeMatchCode() -> Int",
		"fn pythonScopeMatchArmCode() -> Int",
		"scopeKind: pythonScopeBlockCode()",
		"scopeKind: pythonScopeMatchCode()",
		"scopeKind: pythonScopeMatchArmCode()",
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("airepair_python.osty missing scope-kind global-ref guard %q", needle)
		}
	}
	for _, forbidden := range []string{
		"scopeKind: pythonScopeBlock,",
		"scopeKind: pythonScopeMatch,",
		"scopeKind: pythonScopeMatchArm,",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("airepair_python.osty reintroduced return-struct scope global ref %q", forbidden)
		}
	}
}

func TestSelfhostPolicyConstantsAvoidGlobalRefUseSites(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs root: %v", err)
	}
	cases := []struct {
		path      string
		needles   []string
		forbidden []string
	}{
		{
			path: "toolchain/airepair_rewrite.osty",
			needles: []string{
				"fn airepairSemanticIndentStepValue() -> String",
				"let bodyIndent = indent + airepairSemanticIndentStepValue()",
			},
			forbidden: []string{"indent + airepairSemanticIndentStep\n"},
		},
		{
			path: "toolchain/format_escape.osty",
			needles: []string{
				"fn formatHexDigitsUpperValue() -> String",
				"strings.slice(formatHexDigitsUpperValue(), nibble, nibble + 1)",
			},
			forbidden: []string{"strings.slice(formatHexDigitsUpper,"},
		},
		{
			path: "toolchain/ci.osty",
			needles: []string{
				"fn ciCheckFormatName() -> CheckName",
				"rep.Checks.push(skipped(ciCheckFormatName()))",
				"checkFromHostResult(ciCheckLockfileName(), host.CheckLockfile(self.Root, self.Manifest))",
			},
			forbidden: []string{
				"skipped(CheckFormat)",
				"skipped(CheckLint)",
				"checkFromHostResult(CheckFormat",
				"Name: CheckPolicy",
			},
		},
		{
			path: "toolchain/diag_render.osty",
			needles: []string{
				"fn diagSeverityErrorCode() -> Int",
				"if sev == diagSeverityErrorCode()",
			},
			forbidden: []string{
				"sev == diagSeverityError {",
				"sev == diagSeverityWarning {",
				"sev == diagSeverityNote {",
			},
		},
		{
			path: "toolchain/pkg_policy.osty",
			needles: []string{
				"fn pkgSourceKindPathCode() -> Int",
				"if kind == pkgSourceKindPathCode()",
			},
			forbidden: []string{
				"kind == pkgSourceKindPath {",
				"kind == pkgSourceKindGit {",
				"kind == pkgSourceKindRegistry {",
			},
		},
		{
			path: "toolchain/scaffold_policy.osty",
			needles: []string{
				"fn scaffoldFixtureCasesDefaultValue() -> Int",
				"if requested > scaffoldFixtureCasesMaxValue()",
			},
			forbidden: []string{
				"requested > scaffoldFixtureCasesMax {",
				"count: scaffoldFixtureCasesDefault,",
			},
		},
	}
	for _, tc := range cases {
		src, err := os.ReadFile(filepath.Join(root, tc.path))
		if err != nil {
			t.Fatalf("read %s: %v", tc.path, err)
		}
		text := string(src)
		for _, needle := range tc.needles {
			if !strings.Contains(text, needle) {
				t.Fatalf("%s missing global-ref use-site guard %q", tc.path, needle)
			}
		}
		for _, forbidden := range tc.forbidden {
			if strings.Contains(text, forbidden) {
				t.Fatalf("%s reintroduced direct policy-global use %q", tc.path, forbidden)
			}
		}
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

func TestVerifySelfRebuildRequiresSourceCompilerStages(t *testing.T) {
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
		`build_self_binary "$stage1" "stage2-seed" "$stage2_seed" "$host_guard"`,
		`build_self_binary "$stage2_seed" "stage2" "$stage2" "$host_guard"`,
		`build_self_binary "$stage2" "stage3" "$stage3" "$host_guard"`,
		`"osty-self source compiler: enabled"`,
		`fail "$label selfhost doctor did not report the source compiler pipeline"`,
		`fail "$label source compiler probe failed"`,
		`fn main() -> Int {`,
		`return x + 1`,
	} {
		if !strings.Contains(text, needle) {
			t.Fatalf("verify-self-rebuild no longer requires source compiler stage marker %q", needle)
		}
	}
	for _, forbidden := range []string{
		"build_self_binary_with_host_mir_backend",
		"build_self_binary_with_compile_driver",
		"bundle_selfhost_driver_source",
		"osty-self MIR JSON backend: enabled",
		"MIR JSON -> LIR Proto backend",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("verify-self-rebuild still allows MIR-JSON/backend-only ratchet path %q", forbidden)
		}
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
