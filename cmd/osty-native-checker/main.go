package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/osty/osty/internal/selfhost"
)

type checkRequest struct {
	Source  string                      `json:"source,omitempty"`
	Package *selfhost.PackageCheckInput `json:"package,omitempty"`
}

type checkSummary struct {
	Assignments     int                       `json:"assignments"`
	Accepted        int                       `json:"accepted"`
	Errors          int                       `json:"errors"`
	ErrorsByContext map[string]int            `json:"errorsByContext,omitempty"`
	ErrorDetails    map[string]map[string]int `json:"errorDetails,omitempty"`
}

type checkedNode struct {
	Node     int    `json:"node"`
	Kind     string `json:"kind"`
	TypeName string `json:"typeName"`
	Start    int    `json:"start"`
	End      int    `json:"end"`
}

type checkedBinding struct {
	Node     int    `json:"node"`
	Name     string `json:"name"`
	TypeName string `json:"typeName"`
	Mutable  bool   `json:"mutable"`
	Start    int    `json:"start"`
	End      int    `json:"end"`
}

type checkedSymbol struct {
	Node     int    `json:"node"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Owner    string `json:"owner"`
	TypeName string `json:"typeName"`
	Start    int    `json:"start"`
	End      int    `json:"end"`
}

type checkInstantiation struct {
	Node       int      `json:"node"`
	Callee     string   `json:"callee"`
	TypeArgs   []string `json:"typeArgs"`
	ResultType string   `json:"resultType"`
	Start      int      `json:"start"`
	End        int      `json:"end"`
}

type checkDiagnostic struct {
	Code     string   `json:"code"`
	Severity string   `json:"severity"`
	Message  string   `json:"message"`
	Start    int      `json:"start"`
	End      int      `json:"end"`
	Notes    []string `json:"notes,omitempty"`
}

type checkResponse struct {
	Summary        checkSummary         `json:"summary"`
	TypedNodes     []checkedNode        `json:"typedNodes"`
	Bindings       []checkedBinding     `json:"bindings"`
	Symbols        []checkedSymbol      `json:"symbols"`
	Instantiations []checkInstantiation `json:"instantiations"`
	Diagnostics    []checkDiagnostic    `json:"diagnostics,omitempty"`
}

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(stdin io.Reader, stdout io.Writer) error {
	var req checkRequest
	if err := json.NewDecoder(stdin).Decode(&req); err != nil {
		return fmt.Errorf("decode checker request: %w", err)
	}
	var (
		checked selfhost.CheckResult
		err     error
	)
	if req.Package != nil {
		checked, err = selfhost.CheckPackageStructured(*req.Package)
		if err != nil {
			return fmt.Errorf("check package request: %w", err)
		}
	} else {
		checked = selfhost.CheckSourceStructured([]byte(req.Source))
	}
	resp := checkResponse{
		Summary: checkSummary{
			Assignments:     checked.Summary.Assignments,
			Accepted:        checked.Summary.Accepted,
			Errors:          checked.Summary.Errors,
			ErrorsByContext: checked.Summary.ErrorsByContext,
			ErrorDetails:    checked.Summary.ErrorDetails,
		},
		TypedNodes:     make([]checkedNode, 0, len(checked.TypedNodes)),
		Bindings:       make([]checkedBinding, 0, len(checked.Bindings)),
		Symbols:        make([]checkedSymbol, 0, len(checked.Symbols)),
		Instantiations: make([]checkInstantiation, 0, len(checked.Instantiations)),
	}
	for _, node := range checked.TypedNodes {
		resp.TypedNodes = append(resp.TypedNodes, checkedNode{
			Node:     node.Node,
			Kind:     node.Kind,
			TypeName: node.TypeName,
			Start:    node.Start,
			End:      node.End,
		})
	}
	for _, binding := range checked.Bindings {
		resp.Bindings = append(resp.Bindings, checkedBinding{
			Node:     binding.Node,
			Name:     binding.Name,
			TypeName: binding.TypeName,
			Mutable:  binding.Mutable,
			Start:    binding.Start,
			End:      binding.End,
		})
	}
	for _, symbol := range checked.Symbols {
		resp.Symbols = append(resp.Symbols, checkedSymbol{
			Node:     symbol.Node,
			Kind:     symbol.Kind,
			Name:     symbol.Name,
			Owner:    symbol.Owner,
			TypeName: symbol.TypeName,
			Start:    symbol.Start,
			End:      symbol.End,
		})
	}
	for _, inst := range checked.Instantiations {
		resp.Instantiations = append(resp.Instantiations, checkInstantiation{
			Node:       inst.Node,
			Callee:     inst.Callee,
			TypeArgs:   append([]string(nil), inst.TypeArgs...),
			ResultType: inst.ResultType,
			Start:      inst.Start,
			End:        inst.End,
		})
	}
	if len(checked.Diagnostics) > 0 {
		resp.Diagnostics = make([]checkDiagnostic, 0, len(checked.Diagnostics))
		for _, d := range checked.Diagnostics {
			resp.Diagnostics = append(resp.Diagnostics, checkDiagnostic{
				Code:     d.Code,
				Severity: d.Severity,
				Message:  d.Message,
				Start:    d.Start,
				End:      d.End,
				Notes:    append([]string(nil), d.Notes...),
			})
		}
	}
	enc := json.NewEncoder(stdout)
	return enc.Encode(resp)
}
