package logscanbench

import "testing"

var logScanSink int64

func BenchmarkLogScan(b *testing.B) {
	for i := 0; i < b.N; i++ {
		logScanSink = LogScan(logLines)
	}
}
