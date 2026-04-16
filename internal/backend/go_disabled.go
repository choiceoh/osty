//go:build !selfhostgen

package backend

import "fmt"

func newGoBackend() (Backend, error) {
	return nil, fmt.Errorf("backend %q has been removed from the public compiler path; use %q", NameGo, NameLLVM)
}
