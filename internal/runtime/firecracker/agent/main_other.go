//go:build !linux

// Stub main for non-Linux builds. The agent only ever runs inside a
// Firecracker microVM and so is never compiled for Darwin or Windows in
// production; this stub exists solely so that `go build ./...` succeeds
// on developer machines.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "fc-agent: this binary only runs on Linux (inside a Firecracker microVM)")
	os.Exit(1)
}
