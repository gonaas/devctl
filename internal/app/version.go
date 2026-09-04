package app

import (
	"runtime/debug"
	"strings"
)

// Version is the effective version string, set from main at startup.
var Version = "dev"

// debugBuildInfo aliases the build info type so tests can substitute the reader.
type debugBuildInfo = debug.BuildInfo

// buildInfoReader is a seam so the fallback path can be tested.
var buildInfoReader = func() (*debugBuildInfo, bool) { return debug.ReadBuildInfo() }

// ResolveVersion determines the effective version string.
//
// Priority: an ldflags override, then the module version recorded in the build
// info, then "dev". This keeps `go install` builds honest without a second
// stamping mechanism.
func ResolveVersion(ldflagsVersion string) string {
	if ldflagsVersion != "dev" && ldflagsVersion != "" {
		return strings.TrimPrefix(ldflagsVersion, "v")
	}
	info, ok := buildInfoReader()
	if !ok {
		return "dev"
	}
	value := info.Main.Version
	if value == "" || value == "(devel)" {
		return "dev"
	}
	return strings.TrimPrefix(value, "v")
}
