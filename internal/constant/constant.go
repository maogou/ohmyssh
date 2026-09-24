// Package constant holds values that are shared across the CLI.
package constant

import "runtime/debug"

// version is the release version, injected at build time:
//
//	go build -ldflags "-X github.com/maogou/ohmyssh/internal/constant.version=1.2.3"
//
// The Makefile passes it, so a binary built by `make build VERSION=1.2.3` knows
// what it is. A plain `go build` leaves it empty.
var version string

// devVersion is what a build carrying no version of its own reports.
const devVersion = "0.1.0"

// Version is the version ohmyssh reports.
//
// There are three answers, in order of preference. What the build injected is
// the release version, and is what a binary from the Makefile has. Otherwise the
// module's own version is used, which is how a `go install ...@v1.2.3` build
// knows what it is — the reason a built-in constant was wrong, since that binary
// would have reported the version it was written with rather than the one it was
// built from. Anything else is a working copy, which has no released version and
// says so by reporting the development one; Go's own marker for that, "(devel)",
// is not a version worth showing anyone.
func Version() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return devVersion
}
