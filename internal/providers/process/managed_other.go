//go:build !windows

package process

// registrySources returns nothing: only Windows has policy registry keys.
func registrySources() []string { return nil }
