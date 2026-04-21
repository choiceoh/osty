package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"runtime/pprof"

	"github.com/osty/osty/internal/backend"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/pipeline"
	"github.com/osty/osty/internal/resolve"
)

// runPipeline implements
//
//	osty pipeline [--json] [--trace] [--per-decl] [--gen]
//	              [--backend NAME] [--emit MODE]
//	              [--diagnostics]
//	              [--cpuprofile PATH] [--memprofile PATH]
//	              FILE-OR-DIR
//
// FILE: lex → parse → resolve → check → lint, with the optional gen
// stage when --gen is set.
//
// DIR: load → resolve → check → lint over every `.osty` file in the
// directory as one package (workspaces aren't supported here — point at
// a leaf package). --gen is logged as skipped because the per-file
// backend gen doesn't aggregate to package output yet.
//
// --per-decl threads check.Opts.OnDecl through, so the table grows a
// per-declaration timing breakdown sorted by total time descending.
//
// --cpuprofile / --memprofile use runtime/pprof so users can drop the
// resulting profile straight into `go tool pprof`. CPU profiling brackets
// the full pipeline; memprofile is captured once the pipeline completes
// (with one explicit `runtime.GC()` first so the snapshot reflects live
// allocations rather than transient garbage).
func runPipeline(args []string) {
	fs := flag.NewFlagSet("pipeline", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr,
			"usage: osty pipeline [--json] [--trace] [--per-decl] [--gen] [--backend NAME] [--emit MODE] [--diagnostics]")
		fmt.Fprintln(os.Stderr,
			"                     [--cpuprofile PATH] [--memprofile PATH] FILE-OR-DIR")
	}
	var (
		jsonOut         bool
		trace           bool
		perDecl         bool
		runGen          bool
		backendName     string
		emitName        string
		showDiagnostics bool
		cpuProfile      string
		memProfile      string
		baseline        string
	)
	fs.BoolVar(&jsonOut, "json", false, "emit machine-readable JSON instead of the text table")
	fs.BoolVar(&trace, "trace", false, "stream a trace line to stderr as each phase completes")
	fs.BoolVar(&perDecl, "per-decl", false, "report per-declaration check timing in the output")
	fs.BoolVar(&runGen, "gen", false, "also run the selected backend as a final pipeline phase")
	fs.StringVar(&backendName, "backend", defaultBackendName(), "code generation backend for --gen (llvm)")
	fs.StringVar(&emitName, "emit", "", "artifact mode for --gen (llvm-ir; default follows backend)")
	fs.BoolVar(&showDiagnostics, "diagnostics", false, "render full diagnostics after the pipeline report")
	fs.StringVar(&cpuProfile, "cpuprofile", "", "write a CPU pprof profile to this path")
	fs.StringVar(&memProfile, "memprofile", "", "write a memory pprof profile to this path after the pipeline")
	fs.StringVar(&baseline, "baseline", "", "compare against a previous --json snapshot at this path")
	_ = fs.Parse(args)
	if fs.NArg() != 1 {
		fs.Usage()
		os.Exit(2)
	}
	backendID := backend.NameLLVM
	emitMode := backend.EmitLLVMIR
	if runGen {
		backendID, emitMode = resolveBackendAndEmitFlags("pipeline", backendName, emitName)
	} else if backendName != defaultBackendName() || emitName != "" {
		fmt.Fprintln(os.Stderr, "osty pipeline: --backend/--emit require --gen")
		os.Exit(2)
	}
	target := fs.Arg(0)

	// CPU profiling brackets the whole pipeline. Defer the Stop so it
	// fires regardless of how runPipeline returns.
	if cpuProfile != "" {
		f, err := os.Create(cpuProfile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "osty pipeline: cpuprofile: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		if err := pprof.StartCPUProfile(f); err != nil {
			fmt.Fprintf(os.Stderr, "osty pipeline: cpuprofile: %v\n", err)
			os.Exit(1)
		}
		defer pprof.StopCPUProfile()
	}

	var stream *os.File
	if trace {
		stream = os.Stderr
	}
	cfg := pipeline.Config{
		PerDecl:    perDecl,
		RunGen:     runGen,
		GenBackend: backendID,
		GenEmit:    emitMode,
	}

	info, err := os.Stat(target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty pipeline: %v\n", err)
		os.Exit(1)
	}

	var r pipeline.Result
	var sourceForDiagnostics []byte
	diagnosticFilename := target
	if info.IsDir() {
		// Workspace vs single-package detection mirrors what `osty
		// check DIR` does: a workspace root has no top-level .osty
		// files but contains subdirectories that do (and/or carries
		// `[workspace]` in its osty.toml).
		if resolve.IsWorkspaceRoot(target, "") {
			r, err = pipeline.RunWorkspace(target, stream, cfg)
		} else {
			r, err = pipeline.RunPackage(target, stream, cfg)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "osty pipeline: %v\n", err)
			os.Exit(1)
		}
	} else {
		selected, handled, err := loadSelectedPackageEntry(target)
		if err != nil {
			fmt.Fprintf(os.Stderr, "osty pipeline: %v\n", err)
			os.Exit(1)
		}
		if handled {
			r = pipeline.RunLoadedPackage(selected.pkg, stream, cfg)
		} else {
			src, err := os.ReadFile(target)
			if err != nil {
				fmt.Fprintf(os.Stderr, "osty pipeline: %v\n", err)
				os.Exit(1)
			}
			sourceForDiagnostics = src
			cfg.GenSourcePath = target
			r = pipeline.RunWithConfig(src, stream, cfg)
		}
	}

	// Baseline mode supersedes the regular table: we render the diff
	// instead so users can spot regressions at a glance. The current
	// run's full snapshot still goes to stdout when --json is also
	// set, so CI can record both diff and updated baseline at once.
	if baseline != "" {
		f, err := os.Open(baseline)
		if err != nil {
			fmt.Fprintf(os.Stderr, "osty pipeline: --baseline: %v\n", err)
			os.Exit(1)
		}
		snap, err := pipeline.LoadSnapshot(f)
		f.Close()
		if err != nil {
			fmt.Fprintf(os.Stderr, "osty pipeline: --baseline: %v\n", err)
			os.Exit(1)
		}
		cmp := pipeline.Compare(snap, r)
		if jsonOut {
			// JSON path keeps the full snapshot — comparison is text-only
			// because the delta semantics are best read by a human.
			if err := r.RenderJSON(os.Stdout); err != nil {
				fmt.Fprintf(os.Stderr, "osty pipeline: %v\n", err)
				os.Exit(1)
			}
			cmp.RenderText(os.Stderr)
		} else {
			cmp.RenderText(os.Stdout)
		}
	} else if jsonOut {
		if err := r.RenderJSON(os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "osty pipeline: %v\n", err)
			os.Exit(1)
		}
	} else {
		r.RenderText(os.Stdout)
	}

	if showDiagnostics && len(r.AllDiags) > 0 {
		formatter := &diag.Formatter{
			Filename: diagnosticFilename,
			Source:   sourceForDiagnostics,
			Sources:  r.Sources,
		}
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, formatter.FormatAll(r.AllDiags))
	}

	// Memory profile after the pipeline finishes. Force a GC first so
	// the snapshot reflects steady-state retained memory rather than
	// transient garbage from the front-end's intermediate maps.
	if memProfile != "" {
		f, err := os.Create(memProfile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "osty pipeline: memprofile: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		runtime.GC()
		if err := pprof.WriteHeapProfile(f); err != nil {
			fmt.Fprintf(os.Stderr, "osty pipeline: memprofile: %v\n", err)
			os.Exit(1)
		}
	}

	// Mirror the exit-code conventions of the other front-end
	// commands: any error-severity diag aborts with exit 1.
	for _, s := range r.Stages {
		if s.Errors > 0 {
			os.Exit(1)
		}
	}
}
