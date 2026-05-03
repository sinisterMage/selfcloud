package firecracker

import (
	"os"
)

// Template describes a pre-built rootfs + kernel pair that powers a microVM.
// selfcloud ships a small library of these (node, python, go) plus accepts
// user-uploaded tarballs.
type Template struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	KernelPath  string `json:"kernelPath"`
	RootFSPath  string `json:"rootfsPath"`
	BootArgs    string `json:"bootArgs"`
	// Bootstrap is a hint shown in the dashboard when a template is
	// missing on disk. The installer (or the operator running
	// `make firecracker-templates`) is expected to materialise it.
	Bootstrap string `json:"bootstrap,omitempty"`
	// Available reflects whether the kernel + rootfs files exist on disk.
	// Populated by Refresh().
	Available bool `json:"available"`
}

// Refresh fills in the Available flag by stat'ing the kernel and rootfs.
func (t *Template) Refresh() {
	_, kerr := os.Stat(t.KernelPath)
	_, rerr := os.Stat(t.RootFSPath)
	t.Available = kerr == nil && rerr == nil
}

// DefaultTemplates is the built-in catalogue, populated at install time when
// the operator opts in to Firecracker. Paths are populated by the installer.
func DefaultTemplates(baseDir string) []Template {
	tpls := []Template{
		{
			Name:        "node-22",
			Description: "Node.js 22 on Alpine",
			KernelPath:  baseDir + "/kernel/vmlinux",
			RootFSPath:  baseDir + "/rootfs/node-22.ext4",
			BootArgs:    "console=ttyS0 reboot=k panic=1 pci=off init=/sbin/init",
			Bootstrap:   "make firecracker-templates  # builds node-22 from node:22-alpine",
		},
		{
			Name:        "python-3.12",
			Description: "Python 3.12 on Alpine",
			KernelPath:  baseDir + "/kernel/vmlinux",
			RootFSPath:  baseDir + "/rootfs/python-3.12.ext4",
			BootArgs:    "console=ttyS0 reboot=k panic=1 pci=off init=/sbin/init",
			Bootstrap:   "make firecracker-templates  # builds python-3.12 from python:3.12-alpine",
		},
		{
			Name:        "go-1.23",
			Description: "Go 1.23 statically linked on Alpine",
			KernelPath:  baseDir + "/kernel/vmlinux",
			RootFSPath:  baseDir + "/rootfs/go-1.23.ext4",
			BootArgs:    "console=ttyS0 reboot=k panic=1 pci=off init=/sbin/init",
			Bootstrap:   "make firecracker-templates  # builds go-1.23 from golang:1.23-alpine",
		},
	}
	for i := range tpls {
		tpls[i].Refresh()
	}
	return tpls
}
