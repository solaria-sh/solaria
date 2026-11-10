package container

import "testing"

func TestShellCommand(t *testing.T) {
	for _, tc := range []struct {
		argv []string
		want string
	}{
		{[]string{"node", "/a b/launch.mjs"}, `node '/a b/launch.mjs'`},
		{[]string{"docker", "run", "--volume", "{workspace}:/workspace", "img:1"}, `docker run --volume "$PWD":/workspace img:1`},
		{[]string{"x", "{workspace}"}, `x "$PWD"`},
		{[]string{"x", "a b/{workspace}/c d"}, `x 'a b/'"$PWD"'/c d'`},
		{[]string{"x", "it's", ""}, `x 'it'\''s' ''`},
	} {
		if got := ShellCommand(tc.argv); got != tc.want {
			t.Errorf("ShellCommand(%q) = %s, want %s", tc.argv, got, tc.want)
		}
	}
}
