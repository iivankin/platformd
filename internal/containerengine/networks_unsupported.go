//go:build !linux || !amd64 || !cgo

package containerengine

func (*Engine) CreateNetwork(NetworkSpec) (Network, error) {
	return Network{}, ErrUnsupported
}

func (*Engine) InspectNetwork(string) (Network, error) {
	return Network{}, ErrUnsupported
}

func (*Engine) RemoveNetwork(string) error {
	return ErrUnsupported
}
