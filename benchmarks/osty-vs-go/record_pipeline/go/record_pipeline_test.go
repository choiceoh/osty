package recordpipelinebench

import "testing"

var recordPipelineSink int

func BenchmarkRecordPipeline(b *testing.B) {
	for i := 0; i < b.N; i++ {
		recordPipelineSink = AnalyzeRecordPipeline(pipelineRecords)
	}
}
