package csvparsebench

import "strings"

var csvRows = buildCSV(512)

// buildCSV emits rows in a realistic "region,tier,user,feature,status"
// shape. All columns are enum-like strings — the realistic CSV ETL hot
// path is overwhelmingly GROUP BY on categorical columns, not numeric
// parsing. Keeping all columns as strings also lets the Osty
// counterpart mirror the fixture verbatim; its LLVM backend doesn't
// yet lower Int→String in interpolation paths.
func buildCSV(n int) []string {
	samples := []string{
		"us,free,alice,search,ok",
		"eu,pro,bob,report,ok",
		"apac,enterprise,carol,search,fail",
		"sa,free,dave,export,ok",
		"us,pro,eve,report,fail",
		"eu,enterprise,frank,search,ok",
		"apac,free,grace,export,ok",
		"sa,pro,heidi,report,fail",
		"us,enterprise,ivan,search,ok",
		"eu,free,judy,export,ok",
		"apac,pro,ken,report,ok",
		"sa,enterprise,leo,search,fail",
	}
	out := make([]string, n)
	for i := 0; i < n; i++ {
		out[i] = samples[i%len(samples)]
	}
	return out
}

func regionWeight(s string) int64 {
	switch s {
	case "us":
		return 1
	case "eu":
		return 2
	case "apac":
		return 3
	case "sa":
		return 4
	}
	return 0
}

func tierWeight(s string) int64 {
	switch s {
	case "free":
		return 1
	case "pro":
		return 3
	case "enterprise":
		return 7
	}
	return 0
}

//go:noinline
func CsvSum(rows []string) int64 {
	counts := make(map[string]int64, 16)
	var total int64
	for _, row := range rows {
		parts := strings.Split(row, ",")
		if len(parts) < 5 {
			continue
		}
		key := parts[0] + "/" + parts[1]
		counts[key]++
		total += regionWeight(parts[0]) * tierWeight(parts[1])
		if parts[4] == "ok" {
			total++
		}
	}
	// Fold the map into the checksum so GROUP BY cost participates.
	for k, v := range counts {
		total += v * int64(len(k))
	}
	return total
}
