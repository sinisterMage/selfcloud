// Package version exposes build metadata baked in via -ldflags.
package version

var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// String returns a human readable build identifier.
func String() string {
	return Version + " (" + Commit + ", " + Date + ")"
}
