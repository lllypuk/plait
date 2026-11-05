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

func TestLoadEnvIgnoresUnknownKeys(t *testing.T) {
	cfg, err := config.Load(sources(t, layers{env: []string{"PLAIT_NOPE=x", "PLAIT_REVIEW_RULES="}}))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if len(cfg.Review.Rules) != 0 {
		t.Errorf("empty PLAIT_REVIEW_RULES must clear the list, got %q", cfg.Review.Rules)
	}
}

func TestLoadFlags(t *testing.T) {
	cfg, err := config.Load(sources(t, layers{flags: map[string]any{"review.rules": []string{"x.md"}}}))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if !slices.Equal(cfg.Review.Rules, []string{"x.md"}) {
		t.Errorf("review.rules = %q", cfg.Review.Rules)
	}

	_, err = config.Load(sources(t, layers{flags: map[string]any{"review.base": 7}}))
	if e, ok := errors.AsType[*output.Error](err); !ok || e.Kind != output.KindInvalid {
		t.Errorf("wrong flag type: err = %v, want Invalid", err)
	}

	_, err = config.Load(sources(t, layers{flags: map[string]any{"review.nope": "x"}}))
	if e, ok := errors.AsType[*output.Error](err); !ok || e.Kind != output.KindInternal {
		t.Errorf("unknown flag key: err = %v, want Internal", err)
	}
}

const (
	maxDiffKey = "review.max_diff_bytes"
	maxDiffEnv = "PLAIT_REVIEW_MAX_DIFF_BYTES"
)

func TestLoadNewKeyDefaults(t *testing.T) {
	cfg, err := config.Load(sources(t, layers{}))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if cfg.Review.MaxDiffBytes != 524288 {
		t.Errorf("review.max_diff_bytes = %d", cfg.Review.MaxDiffBytes)
	}
	if len(cfg.Review.Checks) != 0 {
		t.Errorf("review.checks = %q, want empty", cfg.Review.Checks)
	}
	if !slices.Contains(cfg.Audit.DenyGlobs, ".env") || !slices.Contains(cfg.Audit.DenyGlobs, "**/*.pem") {
		t.Errorf("audit.deny_globs = %q", cfg.Audit.DenyGlobs)
	}
	if cfg.MR.Template != "" {
		t.Errorf("mr.template = %q, want empty", cfg.MR.Template)
	}
}

func TestLoadMaxDiffBytes(t *testing.T) {
	tests := []struct {
		name string
		l    layers
		want int
	}{
		{"from file", layers{repo: "review:\n  max_diff_bytes: 1000\n"}, 1000},
		{"from env", layers{repo: "review:\n  max_diff_bytes: 1000\n", env: []string{maxDiffEnv + "=2000"}}, 2000},
		{"flag string", layers{env: []string{maxDiffEnv + "=2000"}, flags: map[string]any{maxDiffKey: "3000"}}, 3000},
		{"flag int", layers{flags: map[string]any{maxDiffKey: 4000}}, 4000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := config.Load(sources(t, tt.l))
			if err != nil {
				t.Fatalf("config.Load: %v", err)
			}
			if cfg.Review.MaxDiffBytes != tt.want {
				t.Errorf("review.max_diff_bytes = %d, want %d", cfg.Review.MaxDiffBytes, tt.want)
			}
		})
	}
}

func TestLoadMaxDiffBytesNotNumber(t *testing.T) {
	tests := []struct {
		name   string
		l      layers
		source func(config.Sources) string
	}{
		{
			"from file",
			layers{repo: "review:\n  max_diff_bytes: lots\n"},
			func(s config.Sources) string { return s.Repo },
		},
		{"from env", layers{env: []string{maxDiffEnv + "=lots"}}, func(config.Sources) string { return maxDiffEnv }},
		{
			"from flag",
			layers{flags: map[string]any{maxDiffKey: "1e6"}},
			func(config.Sources) string { return maxDiffKey },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := sources(t, tt.l)
			_, err := config.Load(s)
			e, ok := errors.AsType[*output.Error](err)
			if !ok || e.Kind != output.KindInvalid || e.Code != "config_invalid" {
				t.Fatalf("err = %v, want Invalid config_invalid", err)
			}
			if src := tt.source(s); !strings.Contains(e.Message, src) {
				t.Errorf("message %q must name %s", e.Message, src)
			}
		})
	}
}

func TestLoadNewListAndStringKeys(t *testing.T) {
	cfg, err := config.Load(sources(t, layers{
		system: "review:\n  checks: [test]\naudit:\n  deny_globs: [a]\nmr:\n  template: sys.tmpl\n",
		repo:   "review:\n  checks: [test, lint]\n",
		env:    []string{"PLAIT_AUDIT_DENY_GLOBS=**/*.pem,.env"},
		flags:  map[string]any{"mr.template": ".plait/mr.tmpl"},
	}))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if !slices.Equal(cfg.Review.Checks, []string{"test", "lint"}) {
		t.Errorf("review.checks = %q", cfg.Review.Checks)
	}
	if !slices.Equal(cfg.Audit.DenyGlobs, []string{"**/*.pem", ".env"}) {
		t.Errorf("audit.deny_globs = %q", cfg.Audit.DenyGlobs)
	}
	if cfg.MR.Template != ".plait/mr.tmpl" {
		t.Errorf("mr.template = %q", cfg.MR.Template)
	}
}
