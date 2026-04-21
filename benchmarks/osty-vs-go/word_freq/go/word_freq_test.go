package wordfreqbench

import "testing"

var wordFreqSink int

func BenchmarkCorpusIndex(b *testing.B) {
	for i := 0; i < b.N; i++ {
		wordFreqSink = AnalyzeCorpusIndex(leftCorpus, rightCorpus)
	}
}
