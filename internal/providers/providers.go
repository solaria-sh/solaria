// Package providers lists the providers compiled into Solaria.
package providers

import (
	"fmt"
	"slices"
	"strings"

	"github.com/renantarouco/solaria/internal/provider"
	"github.com/renantarouco/solaria/internal/providers/gitagent"
	"github.com/renantarouco/solaria/internal/providers/managedagents"
	"github.com/renantarouco/solaria/internal/providers/stub"
)

var factories = map[string]provider.Factory{
	gitagent.Name:      gitagent.New,
	managedagents.Name: managedagents.New,
	stub.Name:          stub.New,
}

// Names lists the available providers, sorted.
func Names() []string {
	names := make([]string, 0, len(factories))
	for n := range factories {
		names = append(names, n)
	}
	slices.Sort(names)
	return names
}

// New creates the named provider.
func New(name string, opts provider.Options) (provider.Provider, error) {
	f, ok := factories[name]
	if !ok {
		return nil, fmt.Errorf("unknown provider %q; this build has %s", name, strings.Join(Names(), ", "))
	}
	return f(opts)
}
