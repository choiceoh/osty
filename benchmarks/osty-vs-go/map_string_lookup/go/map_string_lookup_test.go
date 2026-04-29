package mapstringlookupbench

import "testing"

var mapStringLookupSink int64

func BenchmarkStringLookup(b *testing.B) {
	for i := 0; i < b.N; i++ {
		mapStringLookupSink = StringLookupChecksum(mapStringIndex, mapStringQueries)
	}
}
