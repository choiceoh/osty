package mirjson

import (
	"testing"

	"github.com/osty/osty/internal/mir"
)

// TestIntrinsicKindWireValuesMatchToolchain pins the integer wire
// values that the toolchain `mir_json.osty::mirJsonIntrinsic` decoder
// expects (toolchain/mir_json.osty:1601+). The Go `IntrinsicKind`
// constants are encoded as `int(x.Kind)` by `fromInstr`, then decoded
// on the LIR Proto subprocess side via the toolchain table. If Go's
// `iota` ordering drifts from the toolchain enum's declaration order,
// every intrinsic from the drift point onward gets misinterpreted at
// decode time — e.g. `strings.indexOf` was being read as
// `string.endsWith` for months because `IntrinsicBytesConcat` sat at
// Go position 86 while the toolchain enum had it at position 7.
//
// The list covers all 138 wire positions (0..=137) that both sides
// share. Positions 138+ are Go-only (`IntrinsicLikely`,
// `IntrinsicUnlikely`) — appended *after* the shared range so the
// completeness assertion below catches anyone who inserts a new
// shared intrinsic in the middle (which would shift `SetClear`'s
// position and break the wire format).
//
// Add a case here whenever a new intrinsic is appended on either
// side; the test forces the two enums to stay aligned at the wire
// boundary.
func TestIntrinsicKindWireValuesMatchToolchain(t *testing.T) {
	cases := []struct {
		kind mir.IntrinsicKind
		wire int
		name string
	}{
		{mir.IntrinsicInvalid, 0, "MirIntrinsicInvalid"},
		{mir.IntrinsicPrint, 1, "MirIntrinsicPrint"},
		{mir.IntrinsicPrintln, 2, "MirIntrinsicPrintln"},
		{mir.IntrinsicEprint, 3, "MirIntrinsicEprint"},
		{mir.IntrinsicEprintln, 4, "MirIntrinsicEprintln"},
		{mir.IntrinsicAbort, 5, "MirIntrinsicAbort"},
		{mir.IntrinsicStringConcat, 6, "MirIntrinsicStringConcat"},
		{mir.IntrinsicBytesConcat, 7, "MirIntrinsicBytesConcat"},
		{mir.IntrinsicChanMake, 8, "MirIntrinsicChanMake"},
		{mir.IntrinsicChanSend, 9, "MirIntrinsicChanSend"},
		{mir.IntrinsicChanRecv, 10, "MirIntrinsicChanRecv"},
		{mir.IntrinsicChanClose, 11, "MirIntrinsicChanClose"},
		{mir.IntrinsicChanIsClosed, 12, "MirIntrinsicChanIsClosed"},
		{mir.IntrinsicTaskGroup, 13, "MirIntrinsicTaskGroup"},
		{mir.IntrinsicSpawn, 14, "MirIntrinsicSpawn"},
		{mir.IntrinsicHandleJoin, 15, "MirIntrinsicHandleJoin"},
		{mir.IntrinsicGroupCancel, 16, "MirIntrinsicGroupCancel"},
		{mir.IntrinsicGroupIsCancelled, 17, "MirIntrinsicGroupIsCancelled"},
		{mir.IntrinsicParallel, 18, "MirIntrinsicParallel"},
		{mir.IntrinsicRace, 19, "MirIntrinsicRace"},
		{mir.IntrinsicCollectAll, 20, "MirIntrinsicCollectAll"},
		{mir.IntrinsicSelect, 21, "MirIntrinsicSelect"},
		{mir.IntrinsicSelectRecv, 22, "MirIntrinsicSelectRecv"},
		{mir.IntrinsicSelectSend, 23, "MirIntrinsicSelectSend"},
		{mir.IntrinsicSelectTimeout, 24, "MirIntrinsicSelectTimeout"},
		{mir.IntrinsicSelectDefault, 25, "MirIntrinsicSelectDefault"},
		{mir.IntrinsicIsCancelled, 26, "MirIntrinsicIsCancelled"},
		{mir.IntrinsicCheckCancelled, 27, "MirIntrinsicCheckCancelled"},
		{mir.IntrinsicYield, 28, "MirIntrinsicYield"},
		{mir.IntrinsicSleep, 29, "MirIntrinsicSleep"},
		{mir.IntrinsicListPush, 30, "MirIntrinsicListPush"},
		{mir.IntrinsicListLen, 31, "MirIntrinsicListLen"},
		{mir.IntrinsicListGet, 32, "MirIntrinsicListGet"},
		{mir.IntrinsicListIsEmpty, 33, "MirIntrinsicListIsEmpty"},
		{mir.IntrinsicListFirst, 34, "MirIntrinsicListFirst"},
		{mir.IntrinsicListLast, 35, "MirIntrinsicListLast"},
		{mir.IntrinsicListSorted, 36, "MirIntrinsicListSorted"},
		{mir.IntrinsicListContains, 37, "MirIntrinsicListContains"},
		{mir.IntrinsicListIndexOf, 38, "MirIntrinsicListIndexOf"},
		{mir.IntrinsicListToSet, 39, "MirIntrinsicListToSet"},
		{mir.IntrinsicListRemoveAt, 40, "MirIntrinsicListRemoveAt"},
		{mir.IntrinsicListReverse, 41, "MirIntrinsicListReverse"},
		{mir.IntrinsicListReversed, 42, "MirIntrinsicListReversed"},
		{mir.IntrinsicListToString, 43, "MirIntrinsicListToString"},
		{mir.IntrinsicMapNew, 44, "MirIntrinsicMapNew"},
		{mir.IntrinsicMapGet, 45, "MirIntrinsicMapGet"},
		{mir.IntrinsicMapSet, 46, "MirIntrinsicMapSet"},
		{mir.IntrinsicMapContains, 47, "MirIntrinsicMapContains"},
		{mir.IntrinsicMapLen, 48, "MirIntrinsicMapLen"},
		{mir.IntrinsicMapKeys, 49, "MirIntrinsicMapKeys"},
		{mir.IntrinsicMapValues, 50, "MirIntrinsicMapValues"},
		{mir.IntrinsicMapRemove, 51, "MirIntrinsicMapRemove"},
		{mir.IntrinsicMapKeysSorted, 52, "MirIntrinsicMapKeysSorted"},
		{mir.IntrinsicMapToString, 53, "MirIntrinsicMapToString"},
		{mir.IntrinsicSetNew, 54, "MirIntrinsicSetNew"},
		{mir.IntrinsicSetInsert, 55, "MirIntrinsicSetInsert"},
		{mir.IntrinsicSetContains, 56, "MirIntrinsicSetContains"},
		{mir.IntrinsicSetLen, 57, "MirIntrinsicSetLen"},
		{mir.IntrinsicSetToList, 58, "MirIntrinsicSetToList"},
		{mir.IntrinsicSetRemove, 59, "MirIntrinsicSetRemove"},
		{mir.IntrinsicSetToString, 60, "MirIntrinsicSetToString"},
		{mir.IntrinsicStringLen, 61, "MirIntrinsicStringLen"},
		{mir.IntrinsicStringIsEmpty, 62, "MirIntrinsicStringIsEmpty"},
		{mir.IntrinsicStringContains, 63, "MirIntrinsicStringContains"},
		{mir.IntrinsicStringCount, 64, "MirIntrinsicStringCount"},
		{mir.IntrinsicStringStartsWith, 65, "MirIntrinsicStringStartsWith"},
		{mir.IntrinsicStringEndsWith, 66, "MirIntrinsicStringEndsWith"},
		{mir.IntrinsicStringIndexOf, 67, "MirIntrinsicStringIndexOf"},
		{mir.IntrinsicStringSplit, 68, "MirIntrinsicStringSplit"},
		{mir.IntrinsicStringTrim, 69, "MirIntrinsicStringTrim"},
		{mir.IntrinsicStringToUpper, 70, "MirIntrinsicStringToUpper"},
		{mir.IntrinsicStringToLower, 71, "MirIntrinsicStringToLower"},
		{mir.IntrinsicStringToInt, 72, "MirIntrinsicStringToInt"},
		{mir.IntrinsicStringToFloat, 73, "MirIntrinsicStringToFloat"},
		{mir.IntrinsicStringReplace, 74, "MirIntrinsicStringReplace"},
		{mir.IntrinsicStringChars, 75, "MirIntrinsicStringChars"},
		{mir.IntrinsicStringBytes, 76, "MirIntrinsicStringBytes"},
		{mir.IntrinsicBytesLen, 77, "MirIntrinsicBytesLen"},
		{mir.IntrinsicBytesIsEmpty, 78, "MirIntrinsicBytesIsEmpty"},
		{mir.IntrinsicBytesGet, 79, "MirIntrinsicBytesGet"},
		{mir.IntrinsicBytesContains, 80, "MirIntrinsicBytesContains"},
		{mir.IntrinsicBytesStartsWith, 81, "MirIntrinsicBytesStartsWith"},
		{mir.IntrinsicBytesEndsWith, 82, "MirIntrinsicBytesEndsWith"},
		{mir.IntrinsicBytesIndexOf, 83, "MirIntrinsicBytesIndexOf"},
		{mir.IntrinsicBytesLastIndexOf, 84, "MirIntrinsicBytesLastIndexOf"},
		{mir.IntrinsicBytesSplit, 85, "MirIntrinsicBytesSplit"},
		{mir.IntrinsicBytesJoin, 86, "MirIntrinsicBytesJoin"},
		{mir.IntrinsicBytesRepeat, 87, "MirIntrinsicBytesRepeat"},
		{mir.IntrinsicBytesReplace, 88, "MirIntrinsicBytesReplace"},
		{mir.IntrinsicBytesReplaceAll, 89, "MirIntrinsicBytesReplaceAll"},
		{mir.IntrinsicBytesTrimLeft, 90, "MirIntrinsicBytesTrimLeft"},
		{mir.IntrinsicBytesTrimRight, 91, "MirIntrinsicBytesTrimRight"},
		{mir.IntrinsicBytesTrim, 92, "MirIntrinsicBytesTrim"},
		{mir.IntrinsicBytesTrimSpace, 93, "MirIntrinsicBytesTrimSpace"},
		{mir.IntrinsicBytesToUpper, 94, "MirIntrinsicBytesToUpper"},
		{mir.IntrinsicBytesToLower, 95, "MirIntrinsicBytesToLower"},
		{mir.IntrinsicBytesToHex, 96, "MirIntrinsicBytesToHex"},
		{mir.IntrinsicBytesSlice, 97, "MirIntrinsicBytesSlice"},
		{mir.IntrinsicOptionIsSome, 98, "MirIntrinsicOptionIsSome"},
		{mir.IntrinsicOptionIsNone, 99, "MirIntrinsicOptionIsNone"},
		{mir.IntrinsicOptionUnwrap, 100, "MirIntrinsicOptionUnwrap"},
		{mir.IntrinsicOptionUnwrapOr, 101, "MirIntrinsicOptionUnwrapOr"},
		{mir.IntrinsicResultIsOk, 102, "MirIntrinsicResultIsOk"},
		{mir.IntrinsicResultIsErr, 103, "MirIntrinsicResultIsErr"},
		{mir.IntrinsicResultUnwrap, 104, "MirIntrinsicResultUnwrap"},
		{mir.IntrinsicResultUnwrapOr, 105, "MirIntrinsicResultUnwrapOr"},
		{mir.IntrinsicRawNull, 106, "MirIntrinsicRawNull"},
		{mir.IntrinsicStringJoin, 107, "MirIntrinsicStringJoin"},
		{mir.IntrinsicListPop, 108, "MirIntrinsicListPop"},
		{mir.IntrinsicStringSubstring, 109, "MirIntrinsicStringSubstring"},
		{mir.IntrinsicByteToInt, 110, "MirIntrinsicByteToInt"},
		{mir.IntrinsicCharToInt, 111, "MirIntrinsicCharToInt"},
		{mir.IntrinsicListSlice, 112, "MirIntrinsicListSlice"},
		{mir.IntrinsicMapGetOr, 113, "MirIntrinsicMapGetOr"},
		{mir.IntrinsicStringSplitInto, 114, "MirIntrinsicStringSplitInto"},
		{mir.IntrinsicStringNthSegment, 115, "MirIntrinsicStringNthSegment"},
		{mir.IntrinsicMapIncr, 116, "MirIntrinsicMapIncr"},
		{mir.IntrinsicStringRepeat, 117, "MirIntrinsicStringRepeat"},
		{mir.IntrinsicStringTrimPrefix, 118, "MirIntrinsicStringTrimPrefix"},
		{mir.IntrinsicStringTrimSuffix, 119, "MirIntrinsicStringTrimSuffix"},
		{mir.IntrinsicStringTrimStart, 120, "MirIntrinsicStringTrimStart"},
		{mir.IntrinsicStringTrimEnd, 121, "MirIntrinsicStringTrimEnd"},
		{mir.IntrinsicStringReplaceAll, 122, "MirIntrinsicStringReplaceAll"},
		{mir.IntrinsicStringSplitN, 123, "MirIntrinsicStringSplitN"},
		{mir.IntrinsicStringFields, 124, "MirIntrinsicStringFields"},
		{mir.IntrinsicStringLastIndexOf, 125, "MirIntrinsicStringLastIndexOf"},
		{mir.IntrinsicBytesFromList, 126, "MirIntrinsicBytesFromList"},
		{mir.IntrinsicBytesFromString, 127, "MirIntrinsicBytesFromString"},
		{mir.IntrinsicBytesToString, 128, "MirIntrinsicBytesToString"},
		{mir.IntrinsicBytesFromHex, 129, "MirIntrinsicBytesFromHex"},
		{mir.IntrinsicIntToByte, 130, "MirIntrinsicIntToByte"},
		{mir.IntrinsicIntToChar, 131, "MirIntrinsicIntToChar"},
		{mir.IntrinsicByteToChar, 132, "MirIntrinsicByteToChar"},
		{mir.IntrinsicCharToByte, 133, "MirIntrinsicCharToByte"},
		{mir.IntrinsicListInsert, 134, "MirIntrinsicListInsert"},
		{mir.IntrinsicListClear, 135, "MirIntrinsicListClear"},
		{mir.IntrinsicMapClear, 136, "MirIntrinsicMapClear"},
		{mir.IntrinsicSetClear, 137, "MirIntrinsicSetClear"},
	}
	for _, c := range cases {
		if int(c.kind) != c.wire {
			t.Errorf("Go IntrinsicKind for %s = %d, want %d (toolchain wire value). Adding a new intrinsic at the wrong position shifts every subsequent intrinsic's wire value and silently miscompiles all source that uses them; see internal/mir/mir.go and toolchain/mir_json.osty for the authoritative alignment.",
				c.name, int(c.kind), c.wire)
		}
	}
	// Completeness sentinel: `IntrinsicSetClear` is the last
	// intrinsic the toolchain table claims (toolchain/mir_json.osty:1739).
	// If anyone inserts a new shared intrinsic in the middle of
	// `internal/mir/mir.go` without re-anchoring this table, the
	// sentinel's wire position drifts and this assertion fires.
	// Likewise, appending a new shared intrinsic between `SetClear`
	// and `Likely` (currently 138/139, Go-only) shifts `SetClear`'s
	// position downward and would also trip here.
	const expectedSharedCount = 138
	if got := len(cases); got != expectedSharedCount {
		t.Fatalf("wire-format case list length = %d, expected %d (one entry per shared toolchain enum slot 0..=137). Update both the case list and `expectedSharedCount` when adding a new shared intrinsic.",
			got, expectedSharedCount)
	}
	if got, want := int(mir.IntrinsicSetClear), expectedSharedCount-1; got != want {
		t.Fatalf("int(IntrinsicSetClear) = %d, want %d. Either a new shared intrinsic was inserted before SetClear (re-anchor the toolchain table + this test), or SetClear was moved (don't — toolchain's mir_json.osty pins its position at 137).",
			got, want)
	}
}
