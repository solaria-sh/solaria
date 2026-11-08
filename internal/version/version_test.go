package version

import (
	"runtime/debug"
	"testing"
)

func TestFromBuildInfo(t *testing.T) {
	vcs := []debug.BuildSetting{
		{Key: "vcs.revision", Value: "06684417ca56f00dbabe0123456789abcdef0123"},
		{Key: "vcs.time", Value: "2026-11-08T10:32:38Z"},
	}
	tests := []struct {
		name                string
		main                string
		settings            []debug.BuildSetting
		version, commit, dt string // set by -ldflags
		want                [3]string
	}{
		{
			name: "go install of a tag",
			main: "v0.1.0",
			want: [3]string{"v0.1.0", "", ""},
		},
		{
			name: "go install of a commit",
			main: "v0.0.0-20261108103238-06684417ca56",
			want: [3]string{"v0.0.0-20261108103238-06684417ca56", "06684417ca56", "2026-11-08T10:32:38Z"},
		},
		{
			name: "pseudo-version after a tag",
			main: "v0.1.1-0.20261108103238-06684417ca56",
			want: [3]string{"v0.1.1-0.20261108103238-06684417ca56", "06684417ca56", "2026-11-08T10:32:38Z"},
		},
		{
			name:     "go build in a dirty checkout",
			main:     "v0.0.0-20261108103238-06684417ca56+dirty",
			settings: vcs,
			want:     [3]string{"v0.0.0-20261108103238-06684417ca56+dirty", "06684417ca56f00dbabe0123456789abcdef0123", "2026-11-08T10:32:38Z"},
		},
		{
			name:     "no module version",
			main:     "(devel)",
			settings: vcs,
			want:     [3]string{"dev", "06684417ca56f00dbabe0123456789abcdef0123", "2026-11-08T10:32:38Z"},
		},
		{
			name:     "ldflags win",
			main:     "v0.0.0-20261108103238-06684417ca56",
			settings: vcs,
			version:  "v0.1.0", commit: "abc", dt: "2026-11-09T00:00:00Z",
			want: [3]string{"v0.1.0", "abc", "2026-11-09T00:00:00Z"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version := tt.version
			if version == "" {
				version = "dev"
			}
			info := &debug.BuildInfo{Main: debug.Module{Version: tt.main}, Settings: tt.settings}
			v, c, d := fromBuildInfo(info, version, tt.commit, tt.dt)
			if got := [3]string{v, c, d}; got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
