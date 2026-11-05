package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lllypuk/plait/internal/config"
	"github.com/lllypuk/plait/internal/output"
)

type layers struct {
	system, user, repo string
	env                []string
	flags              map[string]any
}

func sources(t *testing.T, l layers) config.Sources {
	t.Helper()
	dir := t.TempDir()
	s := config.Sources{
		System:  filepath.Join(dir, "etc", "plait", "config.yaml"),
		User:    filepath.Join(dir, "home", ".config", "plait", "config.yaml"),
		Repo:    filepath.Join(dir, "repo", ".plait", "config.yaml"),
		Environ: l.env,
		Flags:   l.flags,
	}
	for path, body := range map[string]string{s.System: l.system, s.User: l.user, s.Repo: l.repo} {
		if body == "" {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func base(v string) string { return "review:\n  base: " + v + "\n" }

func TestLoadLayerOrder(t *testing.T) {
	all := layers{
		system: base("sys"),
		user:   base("user"),
		repo:   base("repo"),
		env:    []string{"PLAIT_REVIEW_BASE=env"},
		flags:  map[string]any{"review.base": "flag"},
	}
	tests := []struct {
		name string
		drop func(*layers)
		want string
	}{
		{"flags win", func(*layers) {}, "flag"},
		{"env over files", func(l *layers) { l.flags = nil }, "env"},
		{"repo over user", func(l *layers) { l.flags, l.env = nil, nil }, "repo"},
		{"user over system", func(l *layers) { l.flags, l.env, l.repo = nil, nil, "" }, "user"},
		{"system over defaults", func(l *layers) { l.flags, l.env, l.repo, l.user = nil, nil, "", "" }, "sys"},
		{"defaults only", func(l *layers) { *l = layers{} }, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := all
			tt.drop(&l)
			cfg, err := config.Load(sources(t, l))
			if err != nil {
				t.Fatalf("config.Load: %v", err)
			}
			if cfg.Review.Base != tt.want {
				t.Errorf("review.base = %q, want %q", cfg.Review.Base, tt.want)
			}
		})
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := config.Load(sources(t, layers{}))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	want := []string{"AGENTS.md", "CLAUDE.md", ".plait/rules.md"}
	if !slices.Equal(cfg.Review.Rules, want) {
		t.Errorf("review.rules = %q, want %q", cfg.Review.Rules, want)
	}
}

func TestLoadUpperLayerKeepsUnsetKeys(t *testing.T) {
	cfg, err := config.Load(sources(t, layers{system: "review:\n  rules: [sys.md]\n", repo: base("develop")}))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if cfg.Review.Base != "develop" || !slices.Equal(cfg.Review.Rules, []string{"sys.md"}) {
		t.Errorf("got %+v", cfg.Review)
	}
}

func TestLoadEnv(t *testing.T) {
	cfg, err := config.Load(sources(t, layers{env: []string{
		"PLAIT_REVIEW_RULES=a.md,b.md",
		"HOME=/home/x",
		"REVIEW_BASE=not-ours",
	}}))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if !slices.Equal(cfg.Review.Rules, []string{"a.md", "b.md"}) {
		t.Errorf("review.rules = %q", cfg.Review.Rules)
	}
	if cfg.Review.Base != "" {
		t.Errorf("review.base = %q, want empty", cfg.Review.Base)
	}
}

func TestLoadBrokenYAML(t *testing.T) {
	const broken = "review: [base: x\n"
	tests := []struct {
		name string
		l    layers
		path func(config.Sources) string
	}{
		{"system", layers{system: broken}, func(s config.Sources) string { return s.System }},
		{"user", layers{user: broken}, func(s config.Sources) string { return s.User }},
		{"repo", layers{repo: broken}, func(s config.Sources) string { return s.Repo }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := sources(t, tt.l)
			_, err := config.Load(s)
			assertInvalid(t, err, tt.path(s))
		})
	}
}

func TestLoadWrongType(t *testing.T) {
	s := sources(t, layers{repo: "review: oops\n"})
	_, err := config.Load(s)
	assertInvalid(t, err, s.Repo)
}

func assertInvalid(t *testing.T, err error, path string) {
	t.Helper()
	e, ok := errors.AsType[*output.Error](err)
	if !ok || e.Kind != output.KindInvalid {
		t.Fatalf("err = %v, want Invalid", err)
	}
	if !strings.Contains(e.Message, path) || !strings.Contains(e.Fix, path) {
		t.Errorf("message %q / fix %q must name %s", e.Message, e.Fix, path)
	}
}

func TestDefaultSources(t *testing.T) {
	t.Setenv("HOME", "/home/u")
	t.Setenv("XDG_CONFIG_HOME", "")
	s := config.DefaultSources("/work/repo")
	if s.System != "/etc/plait/config.yaml" {
		t.Errorf("System = %q", s.System)
	}
	if s.User != "/home/u/.config/plait/config.yaml" {
		t.Errorf("User = %q", s.User)
	}
	if s.Repo != "/work/repo/.plait/config.yaml" {
		t.Errorf("Repo = %q", s.Repo)
	}

	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if got := config.DefaultSources("/r").User; got != "/xdg/plait/config.yaml" {
		t.Errorf("User with XDG_CONFIG_HOME = %q", got)
	}
}
