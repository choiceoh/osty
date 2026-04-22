package recordpipelinebench

import "sort"

type record struct {
	Service string
	Region  string
	Level   string
	Latency int
	Hot     bool
	Burst   bool
}

var pipelineRecords = buildPipelineRecords()

func buildPipelineRecords() []record {
	services := []string{"compiler", "runtime", "lsp", "checker"}
	regions := []string{"apac", "us", "eu"}
	levels := []string{"info", "warn", "error"}

	rows := make([]record, 0, 192)
	for i := 0; i < 192; i++ {
		rows = append(rows, record{
			Service: services[(i*5+i/3)%len(services)],
			Region:  regions[(i*7+i/5)%len(regions)],
			Level:   levels[(i*11+i/7)%len(levels)],
			Latency: 10 + ((i*17 + i/2) % 29),
			Hot:     i%5 != 2,
			Burst:   i%8 == 0,
		})
	}
	return rows
}

//go:noinline
func AnalyzeRecordPipeline(rows []record) int {
	totals := make(map[string]int, 32)
	for _, row := range rows {
		if !row.Hot || row.Latency < 18 {
			continue
		}
		key := row.Service + "/" + row.Region + "/" + row.Level
		score := row.Latency + len(row.Service) + len(row.Region)
		if row.Burst {
			score += 11
		}
		totals[key] += score
	}

	keys := make([]string, 0, len(totals))
	for key := range totals {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	checksum := len(keys) * 17
	for _, key := range keys {
		checksum += totals[key] + len(key)
	}
	return checksum
}
