// Package version reports the build version of Solaria.
//
// Release builds set the variables through -ldflags, for example in
// GoReleaser:
//
//	-X github.com/solaria-sh/solaria/internal/version.Version=v0.1.0
//
// Builds without them, such as `go install ...@v0.1.0`, fill them in from
// what the Go toolchain records in the binary: the module version, and the
// commit and time from the VCS stamp or a pseudo-version.
package version

import (
	"fmt"
	"regexp"
	"runtime"
	"runtime/debug"
	"time"
)

var (
	// Version is the semantic version of the build, such as v0.1.0, or
	// "dev" when it isn't known.
	Version = "dev"
	// Commit is the git commit the build was made from.
	Commit = ""
	// Date is the build or commit date in RFC 3339 format.
	Date = ""
)

func init() {
	if info, ok := debug.ReadBuildInfo(); ok {
		Version, Commit, Date = fromBuildInfo(info, Version, Commit, Date)
	}
}

// pseudo matches the time and revision at the end of a pseudo-version,
// such as v0.0.0-20261108103238-06684417ca56.
var pseudo = regexp.MustCompile(`[-.](\d{14})-([0-9a-f]{12})(\+.*)?$`)

// fromBuildInfo fills in whatever -ldflags left unset. A version already
// set wins; "(devel)" is what the toolchain records when it has none.
func fromBuildInfo(info *debug.BuildInfo, version, commit, date string) (string, string, string) {
	if version == "dev" {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			version = v
		}
	}
	for _, s := range info.Settings {
		switch {
		case s.Key == "vcs.revision" && commit == "":
			commit = s.Value
		case s.Key == "vcs.time" && date == "":
			date = s.Value
		}
	}
	if m := pseudo.FindStringSubmatch(info.Main.Version); m != nil {
		if commit == "" {
			commit = m[2]
		}
		if t, err := time.Parse("20060102150405", m[1]); date == "" && err == nil {
			date = t.UTC().Format(time.RFC3339)
		}
	}
	return version, commit, date
}

// String returns a one-line, human-readable description of the build.
func String() string {
	commit, date := Commit, Date
	if commit == "" {
		commit = "unknown"
	}
	if date == "" {
		date = "unknown"
	}
	return fmt.Sprintf("solaria %s (commit %s, built %s, %s %s/%s)",
		Version, shorten(commit), date, runtime.Version(), runtime.GOOS, runtime.GOARCH)
}

func shorten(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}
