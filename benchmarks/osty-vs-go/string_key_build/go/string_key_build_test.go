package stringkeybuildbench

import "testing"

var stringKeyBuildSink int64

func BenchmarkStringKeyBuild(b *testing.B) {
	for i := 0; i < b.N; i++ {
		stringKeyBuildSink = BuildStringKeys(4096)
	}
}
