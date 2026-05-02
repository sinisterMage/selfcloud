//go:build linux

package container

func newPlatform(socket string) (Runtime, error) {
	return NewCtr(socket, "selfcloud")
}
