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
	}
	for _, c := range cases {
		if int(c.kind) != c.wire {
			t.Errorf("Go IntrinsicKind for %s = %d, want %d (toolchain wire value). Adding a new intrinsic at the wrong position shifts every subsequent intrinsic's wire value and silently miscompiles all source that uses them; see internal/mir/mir.go and toolchain/mir_json.osty for the authoritative alignment.",
				c.name, int(c.kind), c.wire)
		}
	}
}
