package builder

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/selfcloud/selfcloud/internal/store"
)

// recipe is the resolved build pipeline: a toolchain image + the shell
// commands to run inside it + the relative path to the produced artifact.
type recipe struct {
	Image    string
	Commands []string
	Output   string // relative to the source dir; file (wasm) or dir (firecracker tarball root)
	Language string
}

// detect picks a build recipe for the given source dir. It honours
// explicit overrides on the BuildSpec; otherwise it inspects the
// repository for well-known marker files.
//
// The implementation is intentionally conservative: rather than running
// arbitrary user-controlled commands by default, it picks defaults that
// match the conventions of each ecosystem and lets the user override
// via BuildSpec.Commands.
func detect(srcDir string, fn *store.Function) (*recipe, error) {
	spec := fn.Source.Git.Build
	r := &recipe{
		Image:    spec.BuildImage,
		Output:   spec.Output,
		Language: spec.Language,
	}
	if len(spec.Commands) > 0 {
		r.Commands = spec.Commands
		if r.Image == "" {
			// User supplied commands but no image — pick a generous default.
			r.Image = "docker.io/library/debian:stable-slim"
		}
		if r.Output == "" {
			r.Output = "."
		}
		return r, nil
	}

	// Autodetect.
	switch {
	case fileExists(srcDir, "Cargo.toml"):
		r.Language = "rust"
		if r.Image == "" {
			r.Image = "docker.io/library/rust:1-bookworm"
		}
		r.Commands = []string{
			"sh", "-lc",
			"rustup target add wasm32-wasi >/dev/null 2>&1 || true; " +
				"cd /src && cargo build --release --target wasm32-wasi && " +
				"cp target/wasm32-wasi/release/*.wasm /out/handler.wasm",
		}
		if r.Output == "" {
			r.Output = "/out/handler.wasm"
		}
	case fileExists(srcDir, "go.mod"):
		r.Language = "tinygo"
		if r.Image == "" {
			r.Image = "docker.io/tinygo/tinygo:latest"
		}
		r.Commands = []string{
			"sh", "-lc",
			"cd /src && tinygo build -o /out/handler.wasm -target wasi .",
		}
		if r.Output == "" {
			r.Output = "/out/handler.wasm"
		}
	case fileExists(srcDir, "package.json"):
		r.Language = "node"
		if r.Image == "" {
			r.Image = "docker.io/library/node:22-bookworm"
		}
		// node-based functions are compiled to a tarball that the
		// firecracker runtime extracts as /code; for wasm builds the
		// user must produce a .wasm via their own toolchain (e.g.
		// AssemblyScript) and reference Output below.
		r.Commands = []string{
			"sh", "-lc",
			"cd /src && (npm ci --no-audit --no-fund || npm install --no-audit --no-fund) && " +
				"if npm run | grep -q '^  build'; then npm run build; fi && " +
				"tar -cf /out/code.tar --exclude=node_modules/.cache .",
		}
		if r.Output == "" {
			if fn.Runtime == store.FunctionRuntimeFirecracker {
				r.Output = "/out/code.tar"
			} else {
				// Hopeful default: the project produced dist/handler.wasm.
				r.Output = "dist/handler.wasm"
			}
		}
	case fileExists(srcDir, "requirements.txt"), fileExists(srcDir, "pyproject.toml"):
		r.Language = "python"
		if r.Image == "" {
			r.Image = "docker.io/library/python:3.12-slim-bookworm"
		}
		r.Commands = []string{
			"sh", "-lc",
			"cd /src && (test -f requirements.txt && pip install --no-cache-dir -r requirements.txt -t vendor || true) && " +
				"tar -cf /out/code.tar .",
		}
		if r.Output == "" {
			r.Output = "/out/code.tar"
		}
	default:
		return nil, errors.New("could not detect build pipeline (no go.mod, Cargo.toml, package.json, requirements.txt or pyproject.toml found)")
	}
	return r, nil
}

func fileExists(dir, file string) bool {
	_, err := os.Stat(filepath.Join(dir, file))
	return err == nil
}
