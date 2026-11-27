// Package version exposes the server build version. The variable is set via
// -ldflags "-X privasync/server/internal/version.Version=x.y.z" by the release
// pipeline; falls back to "dev" for local builds.
package version

var Version = "dev"
