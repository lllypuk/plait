package install_test

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lllypuk/plait/internal/agent"
	"github.com/lllypuk/plait/internal/config"
	"github.com/lllypuk/plait/internal/install"
	"github.com/lllypuk/plait/internal/output"
	"github.com/lllypuk/plait/internal/skills"
)

var update = flag.Bool( //nolint:gochecknoglobals // go test -update flag
	"update", false, "rewrite golden files")

func repoPlan(t *testing.T, root string) []install.Op {
	t.Helper()
	ops, err := install.RepoPlan(registry(t).All(), install.Env{Root: root}, v1)
	if err != nil {
		t.Fatal(err)
	}
	return ops
}

func initRepo(t *testing.T, root string) []install.Op {
	t.Helper()
	ops := repoPlan(t, root)
	apply(t, ops)
	return ops
}

func writeRepoFile(t *testing.T, root, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readRepoFile(t *testing.T, root, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s differs (rerun with -update):\n%s", path, got)
	}
}

func TestRepoPlanEmptyRepo(t *testing.T) {
	root := t.TempDir()
	ops := repoPlan(t, root)
	if _, err := os.Stat(filepath.Join(root, install.AgentsFile)); err == nil {
		t.Fatal("RepoPlan wrote files: a dry run must not")
	}
	for _, op := range ops {
		if op.Action != install.ActionCreate {
			t.Errorf("%s %s: action %s, want create", op.Item, op.Path, op.Action)
		}
	}
	apply(t, ops)

	golden(t, "AGENTS.md.golden", []byte(readRepoFile(t, root, install.AgentsFile)))
	golden(t, "gitignore.golden", []byte(readRepoFile(t, root, ".gitignore")))
	for _, d := range registry(t).All() {
		if got := readRepoFile(t, root, d.ContextFile); got != "@AGENTS.md\n" {
			t.Errorf("%s = %q", d.ContextFile, got)
		}
		for _, s := range skills.All() {
			if _, err := os.Stat(filepath.Join(root, d.SkillDirs.Project, s.Name, skills.File)); err != nil {
				t.Errorf("%s: %v", d.Name, err)
			}
		}
		if _, err := os.Stat(filepath.Join(root, d.Permissions.Project)); err != nil {
			t.Errorf("%s deny rules: %v", d.Name, err)
		}
	}
}

func TestRepoPlanRepeat(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	for _, op := range repoPlan(t, root) {
		if op.Changes() {
			t.Errorf("repeat: %s %s %s", op.Action, op.Item, op.Path)
		}
	}
}

func TestRepoPlanKeepsForeignText(t *testing.T) {
	root := t.TempDir()
	agents := "# Team\n\nRun make check.\n"
	ignore := "*.log\n/.plait/*" // last line without a newline
	writeRepoFile(t, root, install.AgentsFile, agents)
	writeRepoFile(t, root, ".gitignore", ignore)
	claude := registry(t).All()[0].ContextFile
	writeRepoFile(t, root, claude, "See @AGENTS.md for rules.\n")

	ops := initRepo(t, root)
	if got := actionOf(ops, install.ItemSection); got != install.ActionUpdate {
		t.Errorf("section action = %s", got)
	}
	got := readRepoFile(t, root, install.AgentsFile)
	if !strings.HasPrefix(got, agents+"\n<!-- plait:begin -->\n") || !strings.HasSuffix(got, "<!-- plait:end -->\n") {
		t.Errorf("AGENTS.md =\n%s", got)
	}
	if got = readRepoFile(t, root, ".gitignore"); got != ignore+"\n!/.plait/config.yaml\n!/.plait/rules.md\n" {
		t.Errorf(".gitignore = %q", got)
	}
	// An import inside a sentence does not count: the agent imports only a line of its own.
	if got = readRepoFile(t, root, claude); got != "See @AGENTS.md for rules.\n\n@AGENTS.md\n" {
		t.Errorf("%s = %q", claude, got)
	}
}

func TestRepoPlanImportNotDuplicated(t *testing.T) {
	root := t.TempDir()
	d := registry(t).All()[0]
	own := "# Rules\n  @AGENTS.md  \nmore\n"
	writeRepoFile(t, root, d.ContextFile, own)
	ops := initRepo(t, root)
	if got := readRepoFile(t, root, d.ContextFile); got != own {
		t.Errorf("%s = %q", d.ContextFile, got)
	}
	for _, op := range ops {
		if op.Item == install.ItemImport && op.Agent == d.Name && op.Action != install.ActionSkip {
			t.Errorf("import action = %s", op.Action)
		}
	}
}

func TestRepoPlanUpdatesOnlySection(t *testing.T) {
	root := t.TempDir()
	before, after := "# Team\n\n", "\n## Later\n\ntext\n"
	writeRepoFile(t, root, install.AgentsFile, before+"<!-- plait:begin -->\nold\n<!-- plait:end -->"+after)
	initRepo(t, root)
	got := readRepoFile(t, root, install.AgentsFile)
	if !strings.HasPrefix(got, before+"<!-- plait:begin -->\n") ||
		!strings.HasSuffix(got, "<!-- plait:end -->"+after) ||
		strings.Contains(got, "old") {
		t.Errorf("AGENTS.md =\n%s", got)
	}
}

func TestRepoPlanBrokenSection(t *testing.T) {
	for name, content := range map[string]string{
		"no end":          "<!-- plait:begin -->\ntext\n",
		"no begin":        "text\n<!-- plait:end -->\n",
		"end first":       "<!-- plait:end -->\n<!-- plait:begin -->\n",
		"two sections":    "<!-- plait:begin -->\n<!-- plait:end -->\n<!-- plait:begin -->\n<!-- plait:end -->\n",
		"begin twice":     "<!-- plait:begin -->\n<!-- plait:begin -->\n<!-- plait:end -->\n",
		"end after twice": "<!-- plait:begin -->\n<!-- plait:end -->\n<!-- plait:end -->\n",
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeRepoFile(t, root, install.AgentsFile, content)
			_, err := install.RepoPlan(registry(t).All(), install.Env{Root: root}, v1)
			e, ok := errors.AsType[*output.Error](err)
			if !ok || e.Kind != output.KindInvalid || e.Code != "agents_section_broken" ||
				!strings.Contains(e.Message, install.AgentsFile) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestRepoPlanKeepsConfig(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".plait"), 0o750); err != nil {
		t.Fatal(err)
	}
	own := "review:\n  checks: [test]\n"
	writeRepoFile(t, root, install.ConfigFile, own)
	if got := actionOf(initRepo(t, root), install.ItemConfig); got != install.ActionSkip {
		t.Errorf("config action = %s", got)
	}
	if got := readRepoFile(t, root, install.ConfigFile); got != own {
		t.Errorf("config = %q", got)
	}
}

// TestConfigTemplateKeepsDefaults guards the template: as created it loads and changes nothing.
func TestConfigTemplateKeepsDefaults(t *testing.T) {
	root := t.TempDir()
	initRepo(t, root)
	if !strings.Contains(readRepoFile(t, root, install.ConfigFile), "# review:") {
		t.Error("config template lost its commented keys")
	}
	got, err := config.Load(config.Sources{Repo: filepath.Join(root, install.ConfigFile)})
	if err != nil {
		t.Fatal(err)
	}
	want, err := config.Load(config.Sources{})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("config from template = %+v, want defaults %+v", got, want)
	}
}

// TestRepoPlanSharedPaths keeps one operation per file when a derived agent shares paths with its base.
func TestRepoPlanSharedPaths(t *testing.T) {
	reg, err := agent.Load(map[string]map[string]any{"derived": {"base": registry(t).All()[0].Name, "cmd": "derived"}})
	if err != nil {
		t.Fatal(err)
	}
	ops, err := install.RepoPlan(reg.All(), install.Env{Root: t.TempDir()}, v1)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, op := range ops {
		if op.Path == "" {
			continue
		}
		key := op.Item + " " + op.Path
		if seen[key] {
			t.Errorf("two operations for %s", key)
		}
		seen[key] = true
	}
}

func TestRepoPlanConflict(t *testing.T) {
	root := t.TempDir()
	d := registry(t).All()[0]
	path := filepath.Join(root, d.SkillDirs.Project, "plait", skills.File)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	writeRepoFile(t, root, filepath.Join(d.SkillDirs.Project, "plait", skills.File), "own\n")
	ops := initRepo(t, root)
	if got := actionOf(ops, "skill plait"); got != install.ActionConflict {
		t.Errorf("action = %s", got)
	}
	if got := readRepoFile(t, root, filepath.Join(d.SkillDirs.Project, "plait", skills.File)); got != "own\n" {
		t.Errorf("own skill overwritten: %q", got)
	}
}

func actionOf(ops []install.Op, item string) install.Action {
	for _, op := range ops {
		if op.Item == item {
			return op.Action
		}
	}
	return ""
}
