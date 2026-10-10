package providers

import (
	"fmt"
	"os"
	"strings"

	providerdefs "github.com/tsbkw/agentlens/examples/providers"
)

// LoadBuiltinProviders loads the provider definitions embedded in the binary, default first.
func LoadBuiltinProviders() ([]*LoadedProvider, error) {
	var loaded []*LoadedProvider
	for _, name := range providerdefs.Order {
		data, err := providerdefs.FS.ReadFile(name)
		if err != nil {
			return nil, fmt.Errorf("failed to read built-in provider %q: %w", name, err)
		}
		p, err := LoadProviderFromBytes(data)
		if err != nil {
			return nil, fmt.Errorf("failed to load built-in provider %q: %w", name, err)
		}
		loaded = append(loaded, p)
	}
	return loaded, nil
}

// ResolveProvider selects a provider by built-in ID or by path to a YAML definition file.
func ResolveProvider(spec string, builtins []*LoadedProvider) (*LoadedProvider, error) {
	for _, p := range builtins {
		if p.Definition.Provider.ID == spec {
			return p, nil
		}
	}
	if fi, err := os.Stat(spec); err == nil && !fi.IsDir() {
		return LoadProviderFromFile(spec)
	}

	ids := make([]string, 0, len(builtins))
	for _, p := range builtins {
		ids = append(ids, p.Definition.Provider.ID)
	}
	return nil, fmt.Errorf("unknown provider %q: use one of [%s] or a path to a provider YAML file", spec, strings.Join(ids, ", "))
}
