//go:build selfhostgen

package backend

func newGoBackend() (Backend, error) {
	return GoBackend{}, nil
}
