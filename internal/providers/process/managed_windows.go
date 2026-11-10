//go:build windows

package process

import "golang.org/x/sys/windows/registry"

// registrySources lists the policy registry keys that hold Claude Code
// managed settings: a Settings value under SOFTWARE\Policies\ClaudeCode in
// HKLM (set by MDM or Group Policy) or HKCU.
func registrySources() []string {
	const path = `SOFTWARE\Policies\ClaudeCode`
	var out []string
	for _, root := range []struct {
		name string
		key  registry.Key
	}{{"HKLM", registry.LOCAL_MACHINE}, {"HKCU", registry.CURRENT_USER}} {
		k, err := registry.OpenKey(root.key, path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		if _, _, err := k.GetStringValue("Settings"); err == nil {
			out = append(out, root.name+`\`+path)
		}
		_ = k.Close()
	}
	return out
}
