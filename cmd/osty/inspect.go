package main

// Helpers for the `osty check --inspect` flag. The inference observations come
// from the selfhost inspect pass; this file only buckets package records by
// owning file and chooses the output format.

import (
	"fmt"
	"os"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/selfhost/api"
)

func runInspectSource(path string, src []byte, flags cliFlags) {
	if !flags.jsonOutput {
		fmt.Printf("# %s\n", path)
	}
	writeInspect(check.InspectSource(src, nil), flags)
}

func runInspectPackageInput(input api.PackageCheckInput, onlyPath string, flags cliFlags) {
	recs, err := selfhost.InspectPackageStructured(input)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty check --inspect: %v\n", err)
		return
	}
	if len(recs) == 0 {
		return
	}
	buckets := make(map[int][]api.InspectRecord, len(input.Files))
	for _, rec := range recs {
		idx := findOwningFile(input.Files, rec.Start)
		if idx < 0 {
			continue
		}
		rel := rec
		rel.Start -= input.Files[idx].Base
		rel.End -= input.Files[idx].Base
		if rel.Start < 0 {
			rel.Start = 0
		}
		if rel.End < rel.Start {
			rel.End = rel.Start
		}
		buckets[idx] = append(buckets[idx], rel)
	}
	for i, f := range input.Files {
		if onlyPath != "" && f.Path != onlyPath {
			continue
		}
		bucket := buckets[i]
		if len(bucket) == 0 {
			continue
		}
		if !flags.jsonOutput {
			label := f.Path
			if label == "" {
				label = f.Name
			}
			fmt.Printf("# %s\n", label)
		}
		writeInspect(check.InspectRecordsFromSelfhost(f.Source, bucket), flags)
	}
}

func writeInspect(recs []check.InspectRecord, flags cliFlags) {
	if flags.jsonOutput {
		if err := check.FormatInspectJSON(os.Stdout, recs); err != nil {
			fmt.Fprintf(os.Stderr, "osty check --inspect: %v\n", err)
		}
		return
	}
	if err := check.FormatInspectText(os.Stdout, recs); err != nil {
		fmt.Fprintf(os.Stderr, "osty check --inspect: %v\n", err)
	}
}
