package firecracker

// Template describes a pre-built rootfs + kernel pair that powers a microVM.
// selfcloud ships a small library of these (node, python, go) plus accepts
// user-uploaded tarballs.
type Template struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	KernelPath  string `json:"kernelPath"`
	RootFSPath  string `json:"rootfsPath"`
	BootArgs    string `json:"bootArgs"`
}

// DefaultTemplates is the built-in catalogue, populated at install time when
// the operator opts in to Firecracker. Paths are populated by the installer.
func DefaultTemplates(baseDir string) []Template {
	return []Template{
		{
			Name:        "node-22",
			Description: "Node.js 22 on Alpine 3.19",
			KernelPath:  baseDir + "/kernel/vmlinux",
			RootFSPath:  baseDir + "/rootfs/node-22.ext4",
			BootArgs:    "console=ttyS0 reboot=k panic=1 pci=off",
		},
		{
			Name:        "python-3.12",
			Description: "Python 3.12 on Alpine 3.19",
			KernelPath:  baseDir + "/kernel/vmlinux",
			RootFSPath:  baseDir + "/rootfs/python-312.ext4",
			BootArgs:    "console=ttyS0 reboot=k panic=1 pci=off",
		},
		{
			Name:        "go-1.23",
			Description: "Go 1.23 statically linked on scratch",
			KernelPath:  baseDir + "/kernel/vmlinux",
			RootFSPath:  baseDir + "/rootfs/go-123.ext4",
			BootArgs:    "console=ttyS0 reboot=k panic=1 pci=off",
		},
	}
}
