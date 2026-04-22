package csvparsebench

import "testing"

var csvSink int64

func BenchmarkCsvSum(b *testing.B) {
	for i := 0; i < b.N; i++ {
		csvSink = CsvSum(csvRows)
	}
}
