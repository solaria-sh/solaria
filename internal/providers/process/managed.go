package process

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// ManagedSettings lists the managed settings sources on this machine that
// every Claude Code process obeys, wherever it runs: files in the system
// directory, macOS configuration profiles and, on Windows, the policy
// registry keys. Server-managed settings, pushed when a session signs in
// with an organisation's account, can't be seen from here.
func ManagedSettings() []string {
	return managedSources(machine{
		goos:     runtime.GOOS,
		exists:   fileExists,
		glob:     filepath.Glob,
		getenv:   os.Getenv,
		registry: registrySources,
	})
}

// machine is what managedSources reads, so tests can fake it.
type machine struct {
	goos     string
	exists   func(path string) bool
	glob     func(pattern string) ([]string, error)
	getenv   func(name string) string
	registry func() []string
}

// systemDir is Claude Code's managed settings directory on an OS.
func systemDir(m machine) string {
	switch m.goos {
	case "darwin":
		return "/Library/Application Support/ClaudeCode"
	case "windows":
		pf := m.getenv("ProgramFiles")
		if pf == "" {
			pf = `C:\Program Files`
		}
		return filepath.Join(pf, "ClaudeCode")
	default:
		return "/etc/claude-code"
	}
}

func managedSources(m machine) []string {
	var out []string
	dir := systemDir(m)
	for _, name := range []string{"managed-settings.json", "managed-mcp.json"} {
		if p := filepath.Join(dir, name); m.exists(p) {
			out = append(out, p)
		}
	}
	dropIns, _ := m.glob(filepath.Join(dir, "managed-settings.d", "*.json"))
	for _, p := range dropIns {
		if !strings.HasPrefix(filepath.Base(p), ".") {
			out = append(out, p)
		}
	}
	if m.goos == "darwin" {
		const profile = "com.anthropic.claudecode.plist"
		if p := filepath.Join("/Library/Managed Preferences", profile); m.exists(p) {
			out = append(out, p)
		}
		perUser, _ := m.glob(filepath.Join("/Library/Managed Preferences", "*", profile))
		out = append(out, perUser...)
	}
	if m.registry != nil {
		out = append(out, m.registry()...)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
