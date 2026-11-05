// Package config merges plait settings from defaults, config files, env and flags.
package config

import (
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-viper/mapstructure/v2"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/env/v2"
	"github.com/knadh/koanf/v2"

	"github.com/lllypuk/plait/internal/output"
)

//go:embed defaults.yaml
var defaults []byte

const envPrefix = "PLAIT_"

// Config is the merged plait configuration.
type Config struct {
	Review Review `koanf:"review"`
}

// Review configures review export.
type Review struct {
	Base  string   `koanf:"base"`
	Rules []string `koanf:"rules"`
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

// Load merges all layers; each layer is decoded on its own first, so an error names its source.
func Load(s Sources) (Config, error) {
	k := koanf.New(".")
	def, defErr := yaml.Parser().Unmarshal(defaults)
	if defErr != nil {
		return Config{}, output.Internal(fmt.Errorf("embedded defaults: %w", defErr))
	}
	layers := []layer{{name: "embedded defaults", values: def}}

	for _, path := range []string{s.System, s.User, s.Repo} {
		l, ok, err := fileLayer(path)
		if err != nil {
			return Config{}, err
		}
		if ok {
			layers = append(layers, l)
		}
	}

	envValues, envErr := env.Provider(".", env.Opt{
		Prefix:        envPrefix,
		TransformFunc: envKey,
		EnvironFunc:   func() []string { return s.Environ },
	}).Read()
	if envErr != nil {
		return Config{}, output.Internal(envErr)
	}
	layers = append(layers,
		layer{name: "PLAIT_* environment", values: envValues, fix: "unset or correct the PLAIT_* variable"},
		layer{name: "flags", values: s.Flags, fix: "correct the flag value"},
	)

	for _, l := range layers {
		if err := l.check(); err != nil {
			return Config{}, err
		}
		if err := k.Load(confmap.Provider(l.values, "."), nil); err != nil {
			return Config{}, output.Internal(err)
		}
	}
	return decode(k)
}

type layer struct {
	name   string
	values map[string]any
	fix    string
}

// fileLayer reports ok=false for a path that is empty or does not exist.
func fileLayer(path string) (layer, bool, error) {
	if path == "" {
		return layer{}, false, nil
	}
	b, err := os.ReadFile(filepath.Clean(path))
	if errors.Is(err, fs.ErrNotExist) {
		return layer{}, false, nil
	}
	if err != nil {
		return layer{}, false, output.Internal(fmt.Errorf("read config %s: %w", path, err))
	}
	fix := "fix " + path + " or remove it, then rerun the command"
	values, err := yaml.Parser().Unmarshal(b)
	if err != nil {
		e := output.Invalid("config_invalid", fmt.Sprintf("config %s is not valid YAML: %v", path, err), fix)
		e.Err = err
		return layer{}, false, e
	}
	return layer{name: "config " + path, values: values, fix: fix}, true, nil
}

func (l layer) check() error {
	k := koanf.New(".")
	if err := k.Load(confmap.Provider(l.values, "."), nil); err != nil {
		return output.Internal(err)
	}
	if _, err := decode(k); err != nil {
		e := output.Invalid("config_invalid", fmt.Sprintf("%s: %v", l.name, err), l.fix)
		e.Err = err
		return e
	}
	return nil
}

func decode(k *koanf.Koanf) (Config, error) {
	var cfg Config
	err := k.UnmarshalWithConf("", &cfg, koanf.UnmarshalConf{DecoderConfig: &mapstructure.DecoderConfig{
		DecodeHook:       mapstructure.StringToSliceHookFunc(","),
		WeaklyTypedInput: true,
		Result:           &cfg,
	}})
	return cfg, err
}

// envKey maps PLAIT_REVIEW_BASE to review.base.
func envKey(k, v string) (string, any) {
	return strings.ReplaceAll(strings.ToLower(strings.TrimPrefix(k, envPrefix)), "_", "."), v
}
