package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/osty/osty/internal/selfhost"
)

type resolveRequest struct {
	Source  string                        `json:"source,omitempty"`
	Package *selfhost.PackageResolveInput `json:"package,omitempty"`
}

type resolveSummary struct {
	Symbols           int            `json:"symbols"`
	Refs              int            `json:"refs"`
	TypeRefs          int            `json:"typeRefs"`
	Diagnostics       int            `json:"diagnostics"`
	Unresolved        int            `json:"unresolved"`
	Duplicates        int            `json:"duplicates"`
	SymbolsByKind     map[string]int `json:"symbolsByKind,omitempty"`
	DiagnosticsByCode map[string]int `json:"diagnosticsByCode,omitempty"`
}

type resolvedSymbol struct {
	Node     int    `json:"node"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	TypeName string `json:"typeName"`
	Arity    int    `json:"arity"`
	Depth    int    `json:"depth"`
	Start    int    `json:"start"`
	End      int    `json:"end"`
	Public   bool   `json:"public"`
	File     string `json:"file,omitempty"`
}

type resolvedRef struct {
	Name        string `json:"name"`
	Node        int    `json:"node"`
	Start       int    `json:"start"`
	End         int    `json:"end"`
	File        string `json:"file,omitempty"`
	TargetNode  int    `json:"targetNode"`
	TargetStart int    `json:"targetStart"`
	TargetEnd   int    `json:"targetEnd"`
	TargetFile  string `json:"targetFile,omitempty"`
}

type resolvedTypeRef struct {
	Name  string `json:"name"`
	Node  int    `json:"node"`
	Start int    `json:"start"`
	End   int    `json:"end"`
	File  string `json:"file,omitempty"`
}

type resolveDiagnostic struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Name    string `json:"name,omitempty"`
	Hint    string `json:"hint,omitempty"`
	Node    int    `json:"node"`
	Start   int    `json:"start"`
	End     int    `json:"end"`
	File    string `json:"file,omitempty"`
}

type resolveResponse struct {
	Summary     resolveSummary      `json:"summary"`
	Symbols     []resolvedSymbol    `json:"symbols"`
	Refs        []resolvedRef       `json:"refs"`
	TypeRefs    []resolvedTypeRef   `json:"typeRefs"`
	Diagnostics []resolveDiagnostic `json:"diagnostics,omitempty"`
}

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(stdin io.Reader, stdout io.Writer) error {
	var req resolveRequest
	if err := json.NewDecoder(stdin).Decode(&req); err != nil {
		return fmt.Errorf("decode resolver request: %w", err)
	}
	var (
		resolved selfhost.ResolveResult
		err      error
	)
	if req.Package != nil {
		resolved, err = selfhost.ResolvePackageStructured(*req.Package)
		if err != nil {
			return fmt.Errorf("resolve package request: %w", err)
		}
	} else {
		resolved = selfhost.ResolveSourceStructured([]byte(req.Source))
	}
	resp := resolveResponse{
		Summary: resolveSummary{
			Symbols:           resolved.Summary.Symbols,
			Refs:              resolved.Summary.Refs,
			TypeRefs:          resolved.Summary.TypeRefs,
			Diagnostics:       resolved.Summary.Diagnostics,
			Unresolved:        resolved.Summary.Unresolved,
			Duplicates:        resolved.Summary.Duplicates,
			SymbolsByKind:     resolved.Summary.SymbolsByKind,
			DiagnosticsByCode: resolved.Summary.DiagnosticsByCode,
		},
		Symbols:  make([]resolvedSymbol, 0, len(resolved.Symbols)),
		Refs:     make([]resolvedRef, 0, len(resolved.Refs)),
		TypeRefs: make([]resolvedTypeRef, 0, len(resolved.TypeRefs)),
	}
	for _, sym := range resolved.Symbols {
		resp.Symbols = append(resp.Symbols, resolvedSymbol{
			Node:     sym.Node,
			Name:     sym.Name,
			Kind:     sym.Kind,
			TypeName: sym.TypeName,
			Arity:    sym.Arity,
			Depth:    sym.Depth,
			Start:    sym.Start,
			End:      sym.End,
			Public:   sym.Public,
			File:     sym.File,
		})
	}
	for _, ref := range resolved.Refs {
		resp.Refs = append(resp.Refs, resolvedRef{
			Name:        ref.Name,
			Node:        ref.Node,
			Start:       ref.Start,
			End:         ref.End,
			File:        ref.File,
			TargetNode:  ref.TargetNode,
			TargetStart: ref.TargetStart,
			TargetEnd:   ref.TargetEnd,
			TargetFile:  ref.TargetFile,
		})
	}
	for _, ref := range resolved.TypeRefs {
		resp.TypeRefs = append(resp.TypeRefs, resolvedTypeRef{
			Name:  ref.Name,
			Node:  ref.Node,
			Start: ref.Start,
			End:   ref.End,
			File:  ref.File,
		})
	}
	if len(resolved.Diagnostics) > 0 {
		resp.Diagnostics = make([]resolveDiagnostic, 0, len(resolved.Diagnostics))
		for _, d := range resolved.Diagnostics {
			resp.Diagnostics = append(resp.Diagnostics, resolveDiagnostic{
				Code:    d.Code,
				Message: d.Message,
				Name:    d.Name,
				Hint:    d.Hint,
				Node:    d.Node,
				Start:   d.Start,
				End:     d.End,
				File:    d.File,
			})
		}
	}
	return json.NewEncoder(stdout).Encode(resp)
}
