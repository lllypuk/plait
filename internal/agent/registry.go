package agent

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/lllypuk/plait/internal/output"
)

//go:embed builtin/*.yaml
var builtinFiles embed.FS

// Registry holds every known agent by name.
type Registry struct {
	byName map[string]Descriptor
}

// BuiltinFS returns the embedded built-in descriptors, one <name>.yaml per agent.
func BuiltinFS() fs.FS {
	sub, err := fs.Sub(builtinFiles, "builtin")
	if err != nil {
		panic(err) // the embed pattern guarantees the directory
	}
	return sub
}

// Load builds the registry from the built-in descriptors and agents.<name> of the config.
func Load(custom map[string]map[string]any) (*Registry, error) {
	return LoadFS(BuiltinFS(), custom)
}

// LoadFS builds the registry from descriptors in builtin and custom agents; a broken built-in is an internal error.
func LoadFS(builtin fs.FS, custom map[string]map[string]any) (*Registry, error) {
	raws, err := readBuiltin(builtin)
	if err != nil {
		return nil, output.Internal(err)
	}
	r := &Registry{byName: map[string]Descriptor{}}
	for _, name := range sortedKeys(raws) {
		d, derr := decode(raws[name])
		if derr != nil {
			return nil, output.Internal(fmt.Errorf("built-in agent %s: %w", name, derr))
		}
		r.byName[name] = d
	}
	for _, name := range sortedKeys(custom) {
		raw, rerr := resolve(name, raws, custom, nil)
		if rerr != nil {
			return nil, rerr
		}
		d, derr := decode(raw)
		if derr != nil {
			return nil, configError(name, derr)
		}
		r.byName[name] = d
	}
	return r, nil
}

func readBuiltin(fsys fs.FS) (map[string]map[string]any, error) {
	files, err := fs.Glob(fsys, "*.yaml")
	if err != nil {
		return nil, err
	}
	raws := map[string]map[string]any{}
	for _, f := range files {
		b, rerr := fs.ReadFile(fsys, f)
		if rerr != nil {
			return nil, rerr
		}
		var raw map[string]any
		if rerr = yaml.Unmarshal(b, &raw); rerr != nil {
			return nil, fmt.Errorf("built-in agent %s: %w", f, rerr)
		}
		name := strings.TrimSuffix(path.Base(f), ".yaml")
		if raw["name"] != name {
			return nil, fmt.Errorf("built-in agent %s: name %v differs from the file name", f, raw["name"])
		}
		raws[name] = raw
	}
	return raws, nil
}

// resolve merges the agent over its base: the named one, or else the built-in of the same name.
func resolve(name string, builtin, custom map[string]map[string]any, stack []string) (map[string]any, error) {
	c, ok := custom[name]
	if !ok {
		return builtin[name], nil
	}
	stack = append(stack, name)
	if n, has := c["name"]; has && n != name {
		return nil, configError(name, fmt.Errorf("name %v differs from the key; drop name", n))
	}
	parent := builtin[name]
	if b, has := c["base"]; has {
		base, isStr := b.(string)
		switch {
		case !isStr:
			return nil, configError(name, fmt.Errorf("base must be an agent name, got %v", b))
		case slices.Contains(stack, base):
			return nil, configError(stack[0], fmt.Errorf("base cycle %s -> %s", strings.Join(stack, " -> "), base))
		case custom[base] == nil && builtin[base] == nil:
			return nil, configError(name, fmt.Errorf("unknown base %q", base))
		}
		var err error
		if parent, err = resolve(base, builtin, custom, stack); err != nil {
			return nil, err
		}
	}
	out := merge(parent, c)
	delete(out, "base")
	out["name"] = name
	return out, nil
}

// Get returns the agent by name.
func (r *Registry) Get(name string) (Descriptor, bool) {
	d, ok := r.byName[name]
	return d, ok
}

// Names returns agent names in sorted order.
func (r *Registry) Names() []string { return sortedKeys(r.byName) }

// All returns the agents sorted by name.
func (r *Registry) All() []Descriptor {
	out := make([]Descriptor, 0, len(r.byName))
	for _, n := range r.Names() {
		out = append(out, r.byName[n])
	}
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
