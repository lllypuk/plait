package install_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lllypuk/plait/internal/agent"
	"github.com/lllypuk/plait/internal/install"
	"github.com/lllypuk/plait/internal/output"
	"github.com/lllypuk/plait/internal/skills"
)

const (
	v1 = "v0.1.0"
	v2 = "v0.2.0"
)

func registry(t *testing.T) *agent.Registry {
	t.Helper()
	r, err := agent.Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func plan(t *testing.T, d agent.Descriptor, env install.Env, version string, uninstall bool) []install.Op {
	t.Helper()
	ops, err := install.Plan(d, install.ScopeUser, env, version, uninstall)
	if err != nil {
		t.Fatal(err)
	}
	return ops
}

func apply(t *testing.T, ops []install.Op) {
	t.Helper()
	if err := install.Apply(ops); err != nil {
		t.Fatal(err)
	}
}

func actions(ops []install.Op, item string) []install.Action {
	var out []install.Action
	for _, op := range ops {
		if strings.HasPrefix(op.Item, item) {
			out = append(out, op.Action)
		}
	}
	return out
}

func repeat(a install.Action, n int) []install.Action { return slices.Repeat([]install.Action{a}, n) }

func skillPath(env install.Env, d agent.Descriptor, name string) string {
	dir := strings.TrimPrefix(d.SkillDirs.User, "~/")
	return filepath.Join(env.Home, dir, name, skills.File)
}

// installed returns a home with the skills and rules of d installed at v1.
func installed(t *testing.T, d agent.Descriptor) install.Env {
	t.Helper()
	env := install.Env{Home: t.TempDir()}
	apply(t, plan(t, d, env, v1, false))
	return env
}

func TestPlanCreate(t *testing.T) {
	for _, d := range registry(t).All() {
		t.Run(d.Name, func(t *testing.T) {
			env := install.Env{Home: t.TempDir()}
			ops := plan(t, d, env, v1, false)
			if got := actions(ops, "skill"); !slices.Equal(got, repeat(install.ActionCreate, len(skills.All()))) {
				t.Fatalf("first plan = %v", got)
			}
			if _, err := os.Stat(filepath.Join(env.Home, strings.TrimPrefix(d.SkillDirs.User, "~/"))); err == nil {
				t.Fatal("Plan wrote files: a dry run must not")
			}
			apply(t, ops)
			info, err := os.Stat(skillPath(env, d, "plait"))
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o644 {
				t.Fatalf("skill mode = %v", info.Mode().Perm())
			}
			if got := actions(plan(t, d, env, v1, false), ""); !slices.Equal(got,
				repeat(install.ActionSkip, len(skills.All())+1)) {
				t.Fatalf("repeat plan = %v, want all skip", got)
			}
		})
	}
}

func TestPlanUpdate(t *testing.T) {
	for _, d := range registry(t).All() {
		t.Run(d.Name, func(t *testing.T) {
			env := installed(t, d)
			ops := plan(t, d, env, v2, false)
			if got := actions(ops, "skill"); !slices.Equal(got, repeat(install.ActionUpdate, len(skills.All()))) {
				t.Fatalf("new version plan = %v", got)
			}
			if !strings.Contains(ops[0].Detail, v1+" -> "+v2) {
				t.Fatalf("update detail = %q", ops[0].Detail)
			}
			apply(t, ops)
			b, err := os.ReadFile(skillPath(env, d, "plait"))
			if err != nil {
				t.Fatal(err)
			}
			if v, managed := install.Stamp(b); !managed || v != v2 {
				t.Fatalf("Stamp = %q, %v", v, managed)
			}
		})
	}
}

func TestPlanUserFile(t *testing.T) {
	for _, d := range registry(t).All() {
		t.Run(d.Name, func(t *testing.T) {
			env := installed(t, d)
			user := skillPath(env, d, "plait-mr")
			mine := []byte("# my own skill\n")
			if err := os.WriteFile(user, mine, 0o600); err != nil {
				t.Fatal(err)
			}
			ops := plan(t, d, env, v2, false)
			if got := actions(ops, "skill plait-mr"); !slices.Equal(got, []install.Action{install.ActionConflict}) {
				t.Fatalf("plan over a user file = %v", got)
			}
			apply(t, ops)
			assertFile(t, user, mine)

			ops = plan(t, d, env, v2, true)
			want := []install.Action{install.ActionRemove, install.ActionRemove, install.ActionSkip}
			if got := actions(ops, "skill"); !slices.Equal(got, want) {
				t.Fatalf("uninstall plan = %v, want %v", got, want)
			}
			apply(t, ops)
			assertFile(t, user, mine)
			if _, err := os.Stat(filepath.Dir(skillPath(env, d, "plait"))); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("skill directory left after uninstall: %v", err)
			}
			if got := actions(plan(t, d, env, v2, true), ""); !slices.Equal(got,
				repeat(install.ActionSkip, len(skills.All())+1)) {
				t.Fatalf("second uninstall = %v", got)
			}
		})
	}
}

func assertFile(t *testing.T, path string, want []byte) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != string(want) {
		t.Fatalf("%s =\n%s\nwant\n%s", path, b, want)
	}
}

func TestStamp(t *testing.T) {
	tests := []struct {
		name, in, version string
		managed           bool
	}{
		{"managed", "---\nname: x\nplait-managed: true\nplait-version: \"v1\"\n---\nbody\n", "v1", true},
		{"no mark", "---\nname: x\n---\nbody\n", "", false},
		{"mark false", "---\nplait-managed: false\n---\n", "", false},
		{"mark as string", "---\nplait-managed: \"true\"\n---\n", "", false},
		{"no frontmatter", "plait-managed: true\n", "", false},
		{"unclosed", "---\nplait-managed: true\n", "", false},
		{"broken yaml", "---\nplait-managed: [\n---\n", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, m := install.Stamp([]byte(tt.in))
			if v != tt.version || m != tt.managed {
				t.Fatalf("Stamp = %q, %v", v, m)
			}
		})
	}
}

func TestEnvPath(t *testing.T) {
	env := install.Env{Home: "/home/u", Root: "/repo"}
	tests := []struct {
		scope   install.Scope
		in      string
		want    string
		invalid bool
	}{
		{install.ScopeUser, "~/.a/skills", "/home/u/.a/skills", false},
		{install.ScopeUser, "/opt/a/skills", "/opt/a/skills", false},
		{install.ScopeUser, ".a/skills", "", true},
		{install.ScopeProject, ".a/skills", "/repo/.a/skills", false},
		{install.ScopeProject, "../a", "", true},
		{install.ScopeProject, "/abs", "", true},
	}
	for _, tt := range tests {
		t.Run(string(tt.scope)+" "+tt.in, func(t *testing.T) {
			got, err := env.Path("x", tt.scope, tt.in)
			if tt.invalid {
				e := output.AsError(err)
				if e == nil || e.Code != "config_invalid" || !strings.Contains(e.Fix, "agents.x") {
					t.Fatalf("err = %v, want config_invalid", err)
				}
				return
			}
			if err != nil || got != filepath.FromSlash(tt.want) {
				t.Fatalf("Path = %q, %v", got, err)
			}
		})
	}
}

func TestPlanProjectScope(t *testing.T) {
	d, _ := registry(t).Get(registry(t).Names()[0])
	env := install.Env{Home: t.TempDir(), Root: t.TempDir()}
	ops, err := install.Plan(d, install.ScopeProject, env, v1, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range ops {
		if !strings.HasPrefix(op.Path, env.Root) {
			t.Fatalf("project op outside the repository: %+v", op)
		}
	}
}

func TestPlanNoneWriter(t *testing.T) {
	r, err := agent.Load(map[string]map[string]any{"x": {
		"cmd": "x", "context_file": "X.md", "skill_dirs": map[string]any{"user": "~/.x/skills", "project": ".x/skills"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	d, _ := r.Get("x")
	env := install.Env{Home: t.TempDir()}
	ops := plan(t, d, env, v1, false)
	last := ops[len(ops)-1]
	if last.Action != install.ActionWarn || last.Item != install.ItemPermissions || last.Path != "" ||
		last.Detail == "" {
		t.Fatalf("permissions op = %+v, want warn", last)
	}
	if got := actions(plan(t, d, env, v1, true), install.ItemPermissions); len(got) != 0 {
		t.Fatalf("uninstall permissions = %v, want none", got)
	}
}

func TestWritersRegistered(t *testing.T) {
	for _, name := range agent.Writers() {
		if _, ok := install.Writer(name); !ok {
			t.Errorf("writer %q has no installer", name)
		}
	}
}
