// Package secrets reads secret values from an environment's secret source
// (spec.secrets): the process environment, or a dotenv file. Providers read
// a value only to hand it to a host or a container. Solaria never stores
// one: state and plans hold reference names only.
package secrets

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/renantarouco/solaria/internal/api/v1alpha1"
)

// Source reads secrets by name.
type Source struct {
	// Describe says where values come from, for messages.
	Describe string
	lookup   func(name string) (string, bool, error)
}

// Lookup returns a secret's value. A missing or empty secret is not an
// error: ok is false.
func (s Source) Lookup(name string) (value string, ok bool, err error) {
	v, ok, err := s.lookup(name)
	return v, ok && v != "", err
}

// For returns the source an environment spec names, relative to dir, the
// folder of the environment's file. No secrets spec means the process
// environment.
func For(spec *v1alpha1.SecretsSpec, dir string) Source {
	if spec == nil || spec.Source != v1alpha1.SecretsDotenv {
		return Source{Describe: "the environment variables", lookup: func(name string) (string, bool, error) {
			v, ok := os.LookupEnv(name)
			return v, ok, nil
		}}
	}
	path := spec.Path
	if path == "" {
		path = ".env"
	}
	file := filepath.Join(dir, filepath.FromSlash(path))
	return Source{Describe: file, lookup: func(name string) (string, bool, error) {
		b, err := os.ReadFile(file)
		if os.IsNotExist(err) {
			return "", false, nil
		}
		if err != nil {
			return "", false, err
		}
		values, err := ParseDotenv(b)
		if err != nil {
			return "", false, fmt.Errorf("%s: %w", file, err)
		}
		v, ok := values[name]
		return v, ok, nil
	}}
}

// ParseDotenv reads NAME=value lines. It accepts an export prefix, # comments
// and blank lines, single-quoted values taken literally, and double-quoted
// values with Go escapes. Unquoted values end at a " #" comment.
func ParseDotenv(b []byte) (map[string]string, error) {
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(b))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		name, value, ok := strings.Cut(line, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			return nil, fmt.Errorf("line %d: want NAME=value", n)
		}
		value = strings.TrimSpace(value)
		switch {
		case len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'':
			value = value[1 : len(value)-1]
		case len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"':
			v, err := strconv.Unquote(value)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", n, err)
			}
			value = v
		default:
			if i := strings.Index(value, " #"); i >= 0 {
				value = strings.TrimSpace(value[:i])
			}
		}
		out[name] = value
	}
	return out, sc.Err()
}
