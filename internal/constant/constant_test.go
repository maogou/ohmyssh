package constant

import (
	"strings"
	"testing"
)

// A build with no version of its own still has to answer `--version` with
// something: a test binary is exactly that case, since `go test` reports the
// module as "(devel)".
func TestVersionFallsBackToTheDevelopmentOne(t *testing.T) {
	if got := Version(); got != devVersion {
		t.Errorf("Version() = %q with nothing injected, want %q", got, devVersion)
	}
}

// What the build injected wins, which is what makes `make build VERSION=x` mean
// anything. The variable is set directly rather than through -ldflags because
// that is the same write the linker performs.
func TestInjectedVersionIsTheOneReported(t *testing.T) {
	original := version
	t.Cleanup(func() { version = original })

	version = "9.9.9"
	if got := Version(); got != "9.9.9" {
		t.Errorf("Version() = %q, want the injected version", got)
	}
}

// Go's own marker for a build with no version is not one to show the user.
func TestDevelopmentMarkerIsNeverReported(t *testing.T) {
	if strings.Contains(Version(), "(devel)") {
		t.Errorf("Version() = %q, want a version rather than Go's placeholder", Version())
	}
}
