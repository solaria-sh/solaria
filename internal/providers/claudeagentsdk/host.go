package claudeagentsdk

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// Host is what the provider needs from this machine: the runtime that
// starts the launcher, and a way to install the SDK. Tests fake it, so they
// need neither Node, Python nor the network.
type Host interface {
	// Runtime finds the runtime's executable and its version.
	Runtime(ctx context.Context, name string) (Runtime, error)
	// Install installs the SDK into a bundle.
	Install(ctx context.Context, req InstallRequest, log io.Writer) error
	// GOOS is the operating system runs start on.
	GOOS() string
}

// Runtime is an installed Node or Python.
type Runtime struct {
	// Path of the executable.
	Path string
	// Version, such as 20.11.1.
	Version string
}

// InstallRequest is one SDK install.
type InstallRequest struct {
	// Runtime is node or python; Exe is its executable.
	Runtime, Exe string
	// Dir is the bundle, which already holds package.json or
	// requirements.txt.
	Dir string
	// CacheDir keeps downloads across installs.
	CacheDir string
}

// minimum versions of the runtimes the SDK supports.
var minimum = map[string][3]int{
	RuntimeNode:   {18, 14, 0},
	RuntimePython: {3, 10, 0},
}

// checkRuntime says why a runtime can't start the launcher, or nothing.
func checkRuntime(name string, rt Runtime) string {
	got, ok := parseVersion(rt.Version)
	want := minimum[name]
	if !ok {
		return fmt.Sprintf("can't tell the version of %s (%q)", rt.Path, rt.Version)
	}
	for i := range want {
		if got[i] != want[i] {
			if got[i] < want[i] {
				return fmt.Sprintf("%s is %s, and the SDK needs %d.%d or later", rt.Path, rt.Version, want[0], want[1])
			}
			break
		}
	}
	return ""
}

var versionNumbers = regexp.MustCompile(`(\d+)\.(\d+)(?:\.(\d+))?`)

func parseVersion(s string) ([3]int, bool) {
	m := versionNumbers.FindStringSubmatch(s)
	if m == nil {
		return [3]int{}, false
	}
	var v [3]int
	for i := range v {
		v[i], _ = strconv.Atoi(m[i+1])
	}
	return v, true
}

// venvPython is the virtual environment's Python inside a bundle.
func venvPython(goos, bundle string) string {
	if goos == "windows" {
		return filepath.Join(bundle, "venv", "Scripts", "python.exe")
	}
	return filepath.Join(bundle, "venv", "bin", "python")
}

// localHost runs the real node, python and npm.
type localHost struct{}

func (localHost) GOOS() string { return runtime.GOOS }

func (localHost) Runtime(ctx context.Context, name string) (Runtime, error) {
	candidates := []string{"node"}
	if name == RuntimePython {
		// Windows has no python3, and its python can be the Store's stub,
		// which only prints a message.
		candidates = []string{"python3", "python"}
		if runtime.GOOS == "windows" {
			candidates = []string{"python"}
		}
	}
	var errs []string
	for _, c := range candidates {
		path, err := exec.LookPath(c)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		out, err := exec.CommandContext(ctx, path, "--version").CombinedOutput()
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s --version: %v", path, err))
			continue
		}
		return Runtime{Path: path, Version: strings.TrimSpace(string(out))}, nil
	}
	return Runtime{}, fmt.Errorf("%s isn't installed: %s", name, strings.Join(errs, "; "))
}

func (h localHost) Install(ctx context.Context, req InstallRequest, log io.Writer) error {
	switch req.Runtime {
	case RuntimeNode:
		npm, err := exec.LookPath("npm")
		if err != nil {
			return fmt.Errorf("install the SDK: npm isn't installed: %w", err)
		}
		return run(ctx, req.Dir, log, []string{"npm_config_cache=" + filepath.Join(req.CacheDir, "npm")},
			npm, "install", "--no-audit", "--no-fund", "--no-update-notifier", "--loglevel=error")
	case RuntimePython:
		if err := run(ctx, req.Dir, log, nil, req.Exe, "-m", "venv", "venv"); err != nil {
			return err
		}
		py := venvPython(h.GOOS(), req.Dir)
		env := []string{"PIP_CACHE_DIR=" + filepath.Join(req.CacheDir, "pip")}
		if err := run(ctx, req.Dir, log, env, py, "-m", "pip", "install",
			"--disable-pip-version-check", "--no-input", "--require-virtualenv", "-r", "requirements.txt"); err != nil {
			return err
		}
		// The versions pip chose for the SDK's dependencies, for the record.
		var freeze bytes.Buffer
		cmd := exec.CommandContext(ctx, py, "-m", "pip", "freeze", "--disable-pip-version-check")
		cmd.Dir, cmd.Stdout, cmd.Stderr = req.Dir, &freeze, log
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("pip freeze: %w", err)
		}
		return os.WriteFile(filepath.Join(req.Dir, "requirements.lock"), freeze.Bytes(), 0o644)
	}
	return fmt.Errorf("unknown runtime %q", req.Runtime)
}

// run runs a command in dir with extra environment variables, sending its
// output to log.
func run(ctx context.Context, dir string, log io.Writer, env []string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir, cmd.Stdout, cmd.Stderr = dir, log, log
	cmd.Env = append(os.Environ(), env...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", filepath.Base(name), strings.Join(args, " "), err)
	}
	return nil
}
