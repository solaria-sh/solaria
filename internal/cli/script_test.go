package cli

import (
	"flag"
	"os"
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
	testscript.Run(t, testscript.Params{
		Dir:                 "testdata/script",
		UpdateScripts:       *update,
		RequireExplicitExec: true,
	})
}
