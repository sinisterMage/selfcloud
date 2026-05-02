//go:build !linux

package firecracker

func newJailer(_ string) (Runtime, error) { return nil, ErrUnsupported }
