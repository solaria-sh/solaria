// Package version reports the build version of Solaria.
//
// The variables are set at build time through -ldflags, for example by
// GoReleaser:
//
//	-X github.com/solaria-sh/solaria/internal/version.Version=v0.1.0
package version

import (
	"fmt"
	"runtime"
	"runtime/debug"
)

var (
	// Version is the semantic version of the build, or "dev".
	Version = "dev"
	// Commit is the git commit the build was made from.
	Commit = ""
	// Date is the build date in RFC 3339 format.
	Date = ""
)

// String returns a one-line, human-readable description of the build.
func String() string {
	commit, date := Commit, Date
	if commit == "" {
		commit = vcsSetting("vcs.revision")
	}
	if date == "" {
		date = vcsSetting("vcs.time")
	}
	if commit == "" {
		commit = "unknown"
	}
	if date == "" {
		date = "unknown"
	}
	return fmt.Sprintf("solaria %s (commit %s, built %s, %s %s/%s)",
		Version, shorten(commit), date, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}

func vcsSetting(key string) string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return ""
	}
	for _, s := range info.Settings {
		if s.Key == key {
			return s.Value
		}
	}
	return ""
}

func shorten(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}
