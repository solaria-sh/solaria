package cli

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"
)

var update = flag.Bool("update", false, "rewrite expected output in test scripts")

// TestMain lets test scripts run the solaria command in-process.
func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){
		"solaria": func() { os.Exit(Main()) },
	})
}

// TestScripts runs the end-to-end scripts in testdata/script. Each script is
// a txtar archive: commands to run, then the files they run against. Run
// with -update to rewrite expected output after an intended change.
func TestScripts(t *testing.T) {
	examples, err := filepath.Abs("../../examples")
	if err != nil {
		t.Fatal(err)
	}
	testscript.Run(t, testscript.Params{
		Dir: "testdata/script",
		Setup: func(env *testscript.Env) error {
			env.Setenv("EXAMPLES", examples)
			return nil
		},
		Cmds: map[string]func(ts *testscript.TestScript, neg bool, args []string){
			"replace": replaceCmd,
		},
		UpdateScripts:       *update,
		RequireExplicitExec: true,
	})
}

// replaceCmd implements "replace file old new": it replaces the first old
// with new in file, where \n in either means a newline. It fails if old
// isn't there, so a script can't silently test nothing.
func replaceCmd(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 3 {
		ts.Fatalf("usage: replace file old new")
	}
	path := ts.MkAbs(args[0])
	old := strings.ReplaceAll(args[1], `\n`, "\n")
	repl := strings.ReplaceAll(args[2], `\n`, "\n")
	data := ts.ReadFile(args[0])
	if !strings.Contains(data, old) {
		ts.Fatalf("replace: %s has no %q", args[0], old)
	}
	ts.Check(os.WriteFile(path, []byte(strings.Replace(data, old, repl, 1)), 0o644))
}
