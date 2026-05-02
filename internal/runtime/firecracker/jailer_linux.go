//go:build linux

package firecracker

func newJailer(dataDir string) (Runtime, error) { return NewJailer(dataDir) }
