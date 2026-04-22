package hashlookupbench

import "testing"

var hashLookupSink int64

func BenchmarkHashLookup(b *testing.B) {
	for i := 0; i < b.N; i++ {
		hashLookupSink = HashLookupChecksum(hashLookupKeys, hashLookupQueryKs)
	}
}
