//go:build !linux

package container

func newPlatform(_ string) (Runtime, error) {
	return nil, ErrUnsupported
}
