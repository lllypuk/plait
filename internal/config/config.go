// Package config merges plait settings from defaults, config files, env and flags.
package config

import (
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/lllypuk/plait/internal/agent"
	"github.com/lllypuk/plait/internal/output"
)

//go:embed defaults.yaml
var defaults []byte

const envPrefix = "PLAIT_"

// Config is the merged plait configuration.
type Config struct {
	Review  Review  `yaml:"review"`
	Audit   Audit   `yaml:"audit"`
	MR      MR      `yaml:"mr"`
	Network Network `yaml:"network"`
	Jira    Jira    `yaml:"jira"`
	// Agents holds raw plait-agent/v1 descriptors; agent.Load resolves them, env and flags do not set them.
	Agents map[string]map[string]any `yaml:"agents"`
}

// Review configures review export.
type Review struct {
	Base         string   `yaml:"base"`
	Rules        []string `yaml:"rules"`
	MaxDiffBytes int      `yaml:"max_diff_bytes"`
	Checks       []string `yaml:"checks"`
}

// Audit configures the audit log and what never leaves the repository.
type Audit struct {
	DenyGlobs []string `yaml:"deny_globs"`
}

// MR configures mr describe.
type MR struct {
	Template string `yaml:"template"`
}

// Network configures the connections plait makes and checks.
type Network struct {
	// CAFile is a PEM bundle added to the system pool; a relative path starts at the repository root.
	CAFile string `yaml:"ca_file"`
	// Endpoints are the URLs plait doctor --net reaches besides the model endpoints of the agents.
	Endpoints []string `yaml:"endpoints"`
}

// Jira configures how plait ctx gets a task from Jira.
type Jira struct {
	Mode    string `yaml:"mode"`
	MCPTool string `yaml:"mcp_tool"`
}

// Sources lists the layers in ascending priority; a missing or empty file path is skipped.
type Sources struct {
	System  string
	User    string
	Repo    string
	Environ []string
	// Flags maps dotted config keys to values of flags the user set.
	Flags map[string]any
}

// DefaultSources returns the standard layer locations for a repository root.
func DefaultSources(repoRoot string) Sources {
	return Sources{
		System:  "/etc/plait/config.yaml",
		User:    userConfigFile(),
		Repo:    filepath.Join(repoRoot, ".plait", "config.yaml"),
		Environ: os.Environ(),
	}
}

func userConfigFile() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "plait", "config.yaml")
}

// Load decodes the layers in order into one Config: a file sets only the keys it has, a list replaces the lower one.
func Load(s Sources) (Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(defaults, &cfg); err != nil {
		return Config{}, output.Internal(fmt.Errorf("embedded defaults: %w", err))
	}
	for _, path := range []string{s.System, s.User, s.Repo} {
		if err := loadFile(path, &cfg); err != nil {
			return Config{}, err
		}
	}

	keys := map[string]reflect.Value{}
	leaves(reflect.ValueOf(&cfg).Elem(), "", keys)
	byEnv := make(map[string]string, len(keys))
	for k := range keys {
		byEnv[envPrefix+strings.ToUpper(strings.ReplaceAll(k, ".", "_"))] = k
	}
	for _, kv := range s.Environ {
		name, val, _ := strings.Cut(kv, "=")
		k, ok := byEnv[name]
		if !ok {
			continue
		}
		if err := set(keys[k], val); err != nil {
			return Config{}, invalid(name+": "+err.Error(), "unset or correct "+name, err)
		}
	}
	for k, v := range s.Flags {
		f, ok := keys[k]
		if !ok {
			return Config{}, output.Internal(fmt.Errorf("flag sets unknown config key %q", k))
		}
		if err := set(f, v); err != nil {
			return Config{}, invalid("flag for "+k+": "+err.Error(), "correct the flag value", err)
		}
	}
	return cfg, nil
}

// loadFile skips a path that is empty or does not exist.
func loadFile(path string, cfg *Config) error {
	if path == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Clean(path))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return output.Internal(fmt.Errorf("read config %s: %w", path, err))
	}
	lower := cfg.Agents
	cfg.Agents = nil
	if err = yaml.Unmarshal(b, cfg); err != nil {
		fix := "fix " + path + " or remove it, then rerun the command"
		return invalid(fmt.Sprintf("config %s: %v", path, err), fix, err)
	}
	cfg.Agents = agent.Merge(lower, cfg.Agents)
	return nil
}

func invalid(msg, fix string, err error) error {
	e := output.Invalid("config_invalid", msg, fix)
	e.Err = err
	return e
}

// leaves maps dotted yaml keys to the non-struct, non-map fields of v.
func leaves(v reflect.Value, prefix string, out map[string]reflect.Value) {
	t := v.Type()
	for i := range t.NumField() {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("yaml"), ",")
		if name == "" || name == "-" {
			continue
		}
		switch f := v.Field(i); f.Kind() { //nolint:exhaustive // other kinds are leaves
		case reflect.Struct:
			leaves(f, prefix+name+".", out)
		case reflect.Map:
		default:
			out[prefix+name] = f
		}
	}
}

// set assigns an env or flag value; a string for a list field is split on commas, for an int field parsed as decimal.
func set(f reflect.Value, v any) error {
	switch f.Interface().(type) {
	case string:
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("want a string, got %T", v)
		}
		f.SetString(s)
	case int:
		switch x := v.(type) {
		case int:
			f.SetInt(int64(x))
		case string:
			n, err := strconv.Atoi(x)
			if err != nil {
				return fmt.Errorf("want a decimal integer, got %q", x)
			}
			f.SetInt(int64(n))
		default:
			return fmt.Errorf("want a decimal integer, got %T", v)
		}
	case []string:
		switch x := v.(type) {
		case string:
			list := []string{}
			if x != "" {
				list = strings.Split(x, ",")
			}
			f.Set(reflect.ValueOf(list))
		case []string:
			f.Set(reflect.ValueOf(slices.Clone(x)))
		default:
			return fmt.Errorf("want a comma-separated list, got %T", v)
		}
	default:
		return fmt.Errorf("unsupported config field type %s", f.Type())
	}
	return nil
}
