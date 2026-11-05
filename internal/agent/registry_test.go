package agent_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"go.yaml.in/yaml/v3"

	"github.com/lllypuk/plait/internal/agent"
	"github.com/lllypuk/plait/internal/output"
)

func custom(t *testing.T, body string) map[string]map[string]any {
	t.Helper()
	var m map[string]map[string]any
	if err := yaml.Unmarshal([]byte(body), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func load(t *testing.T, body string) *agent.Registry {
	t.Helper()
	r, err := agent.Load(custom(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return r
}

func get(t *testing.T, r *agent.Registry, name string) agent.Descriptor {
	t.Helper()
	d, ok := r.Get(name)
	if !ok {
		t.Fatalf("agent %q not in registry %v", name, r.Names())
	}
	return d
}

const (
	qwenSkills = "~/.qwen/skills"
	claude     = "claude"
	qwen       = "qwen"
	cycleMsg   = "base cycle"
)

func TestBuiltin(t *testing.T) {
	r := load(t, "")
	if got := r.Names(); !slices.Equal(got, []string{claude, qwen}) {
		t.Fatalf("Names() = %v", got)
	}
	tests := []struct {
		name, cmd, user, project, context, writer, endpoint string
		frontmatter                                         bool
	}{
		{claude, claude, "~/.claude/skills", ".claude/skills", "CLAUDE.md", agent.WriterClaudeSettings,
			"ANTHROPIC_BASE_URL", true},
		{qwen, qwen, qwenSkills, ".qwen/skills", "QWEN.md", agent.WriterQwenSettings, "OPENAI_BASE_URL", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := get(t, r, tt.name)
			got := agent.Descriptor{
				Name: d.Name, Cmd: d.Cmd, SkillDirs: d.SkillDirs, ContextFile: d.ContextFile,
				Permissions: agent.Permissions{Writer: d.Permissions.Writer}, ModelEndpointEnv: d.ModelEndpointEnv,
			}
			want := agent.Descriptor{
				Name:             tt.name,
				Cmd:              tt.cmd,
				SkillDirs:        agent.Scopes{User: tt.user, Project: tt.project},
				ContextFile:      tt.context,
				Permissions:      agent.Permissions{Writer: tt.writer},
				ModelEndpointEnv: tt.endpoint,
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("descriptor = %+v", d)
			}
			if got := d.SkillFrontmatter["disable-model-invocation"] == true; got != tt.frontmatter {
				t.Fatalf("disable-model-invocation = %v, want %v", got, tt.frontmatter)
			}
			if len(d.TelemetryOff) == 0 {
				t.Fatal("no telemetry_off conditions")
			}
			if !slices.Equal(d.VersionArgs, []string{versionFlag}) {
				t.Fatalf("VersionArgs = %v", d.VersionArgs)
			}
		})
	}
	if d := get(t, r, claude); !slices.Equal(d.Permissions.Deny, []string{"plait approve", "plait reject"}) {
		t.Fatalf("claude deny = %v", d.Permissions.Deny)
	}
}

func TestCustomWithoutBase(t *testing.T) {
	r := load(t, `
mine:
  cmd: mine-cli
  skill_dirs: { user: ~/.mine/skills, project: .mine/skills }
  context_file: MINE.md
`)
	d := get(t, r, "mine")
	if d.Cmd != "mine-cli" || d.ContextFile != "MINE.md" || d.SkillDirs.Project != ".mine/skills" {
		t.Fatalf("descriptor = %+v", d)
	}
	if d.Permissions.Writer != agent.WriterNone {
		t.Fatalf("writer = %q, want none by default", d.Permissions.Writer)
	}
	if !slices.Equal(d.VersionArgs, []string{versionFlag}) {
		t.Fatalf("VersionArgs = %v, want the default", d.VersionArgs)
	}
	if got := r.Names(); !slices.Equal(got, []string{claude, "mine", qwen}) {
		t.Fatalf("Names() = %v", got)
	}
}

func TestDerivedViaBase(t *testing.T) {
	r := load(t, `
sandbox:
  base: claude
  cmd: claude-sandbox
  skill_dirs: { user: ~/.sandbox/skills }
  telemetry_off:
    - env: SANDBOX_OFFLINE
`)
	d := get(t, r, "sandbox")
	if d.Name != "sandbox" || d.Cmd != "claude-sandbox" {
		t.Fatalf("descriptor = %+v", d)
	}
	if d.SkillDirs.User != "~/.sandbox/skills" || d.SkillDirs.Project != ".claude/skills" {
		t.Fatalf("skill_dirs not merged: %+v", d.SkillDirs)
	}
	if d.ContextFile != "CLAUDE.md" || d.Permissions.Writer != agent.WriterClaudeSettings ||
		d.ModelEndpointEnv != "ANTHROPIC_BASE_URL" {
		t.Fatalf("base fields not inherited: %+v", d)
	}
	if len(d.TelemetryOff) != 1 || d.TelemetryOff[0].Env != "SANDBOX_OFFLINE" {
		t.Fatalf("telemetry_off not replaced: %+v", d.TelemetryOff)
	}
	if get(t, r, claude).Cmd != "claude" {
		t.Fatal("base descriptor changed by the derived one")
	}
}

func TestDerivedChain(t *testing.T) {
	r := load(t, `
fork:
  base: sandbox
  cmd: fork-cli
sandbox:
  base: qwen
  context_file: SANDBOX.md
`)
	d := get(t, r, "fork")
	if d.Cmd != "fork-cli" || d.ContextFile != "SANDBOX.md" || d.SkillDirs.User != qwenSkills {
		t.Fatalf("descriptor = %+v", d)
	}
}

func TestOverrideBuiltin(t *testing.T) {
	r := load(t, `
qwen:
  cmd: /opt/qwen/bin/qwen
  skill_dirs: { project: .agents/skills }
`)
	d := get(t, r, qwen)
	if d.Cmd != "/opt/qwen/bin/qwen" || d.SkillDirs.Project != ".agents/skills" || d.SkillDirs.User != qwenSkills {
		t.Fatalf("descriptor = %+v", d)
	}
	if d.ContextFile != "QWEN.md" {
		t.Fatalf("context_file = %q, want the builtin one", d.ContextFile)
	}
}

func TestLoadInvalid(t *testing.T) {
	tests := []struct {
		name, body, agent, want string
	}{
		{"unknown base", "x:\n  base: nope\n", "x", `unknown base "nope"`},
		{"two-step cycle", "a:\n  base: b\nb:\n  base: a\n", "a", cycleMsg},
		{"self cycle", "a:\n  base: a\n", "a", cycleMsg},
		{"unknown writer", "claude:\n  permissions: { writer: vim }\n", "claude", `unknown writer "vim"`},
		{"missing cmd", "x:\n  skill_dirs: { user: ~/x, project: .x }\n  context_file: X.md\n", "x", "cmd"},
		{"missing skill_dirs", "x:\n  cmd: x\n  context_file: X.md\n", "x", "skill_dirs"},
		{"missing context_file", "x:\n  cmd: x\n  skill_dirs: { user: ~/x, project: .x }\n", "x", "context_file"},
		{"writer without deny", "x:\n  base: claude\n  permissions: { deny: [] }\n", "x", "deny"},
		{"unknown field", "x:\n  base: claude\n  colour: red\n", "x", "colour"},
		{"bad name", "X:\n  base: claude\n", "X", "name"},
		{"name differs", "x:\n  base: claude\n  name: y\n", "x", "name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := agent.Load(custom(t, tt.body))
			e, ok := errors.AsType[*output.Error](err)
			if !ok || e.Kind != output.KindInvalid || e.Code != "config_invalid" {
				t.Fatalf("err = %#v, want Invalid config_invalid", err)
			}
			if !strings.Contains(e.Message, "agents."+tt.agent) || !strings.Contains(e.Message, tt.want) {
				t.Fatalf("message %q lacks agents.%s or %q", e.Message, tt.agent, tt.want)
			}
			if !strings.Contains(e.Fix, "agents."+tt.agent) {
				t.Fatalf("fix %q does not name agents.%s", e.Fix, tt.agent)
			}
		})
	}
}

// TestThirdAgentFromYAML adds an agent with a known writer as one YAML file: no Go code names it.
func TestThirdAgentFromYAML(t *testing.T) {
	fsys := fstest.MapFS{}
	err := fs.WalkDir(agent.BuiltinFS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(agent.BuiltinFS(), path)
		fsys[path] = &fstest.MapFile{Data: b}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	third, err := os.ReadFile(filepath.Join("testdata", "third.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	fsys["third.yaml"] = &fstest.MapFile{Data: third}

	r, err := agent.LoadFS(fsys, nil)
	if err != nil {
		t.Fatalf("LoadFS: %v", err)
	}
	d := get(t, r, "third")
	dir := fakeAgent(t, "third-cli", "echo 'third 0.9.1'")
	t.Setenv("PATH", dir)
	got, err := d.Detect(t.Context())
	if err != nil || !got.Installed || got.Version != "third 0.9.1" {
		t.Fatalf("Detect = %+v, %v", got, err)
	}
}

func TestBuiltinNameMatchesFile(t *testing.T) {
	fsys := fstest.MapFS{"one.yaml": &fstest.MapFile{Data: []byte(
		"name: two\ncmd: x\nskill_dirs: { user: ~/x, project: .x }\ncontext_file: X.md\n")}}
	_, err := agent.LoadFS(fsys, nil)
	if e, ok := errors.AsType[*output.Error](err); !ok || e.Kind != output.KindInternal {
		t.Fatalf("err = %#v, want Internal", err)
	}
}
