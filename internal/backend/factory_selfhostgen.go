//go:build selfhostgen

package backend

import "fmt"

// New returns the concrete backend implementation for name.
func New(name Name) (Backend, error) {
	switch name {
	case NameGo:
		return GoBackend{}, nil
	case NameLLVM:
		return LLVMBackend{}, nil
	default:
		return nil, fmt.Errorf("unknown backend %q", name)
	}
}
