package skills_test

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/lllypuk/plait/internal/agent"
	"github.com/lllypuk/plait/internal/output"
	"github.com/lllypuk/plait/internal/run"
	"github.com/lllypuk/plait/internal/skills"
)

var update = flag.Bool( //nolint:gochecknoglobals // go test -update flag
	"update", false, "rewrite golden files")

const version = "v0.1.0-3-gabcdef0"

func TestRenderGolden(t *testing.T) {
	reg, err := agent.Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range reg.All() {
		for _, s := range skills.All() {
			t.Run(d.Name+"/"+s.Name, func(t *testing.T) { checkRendered(t, d, s) })
		}
	}
}

func checkRendered(t *testing.T, d agent.Descriptor, s skills.Skill) {
	t.Helper()
	got, err := skills.Render(d, s, version)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(got), "\n"); n < 20 || n > 40 {
		t.Errorf("%d lines, want 20-40", n)
	}
	fm := frontmatter(t, got)
	if fm["name"] != s.Name || fm[skills.KeyManaged] != true || fm[skills.KeyVersion] != version {
		t.Errorf("frontmatter = %v", fm)
	}
	if desc, _ := fm["description"].(string); desc == "" {
		t.Error("no description")
	}
	golden(t, d.Name+"-"+s.Name+".md.golden", got)
}

func TestDisableModelInvocationOnlyForClaudeStageSkills(t *testing.T) {
	reg, err := agent.Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"claude", "qwen"} {
		d, ok := reg.Get(name)
		if !ok {
			t.Fatalf("no built-in %s", name)
		}
		for _, s := range skills.All() {
			got, rerr := skills.Render(d, s, version)
			if rerr != nil {
				t.Fatal(rerr)
			}
			_, has := frontmatter(t, got)["disable-model-invocation"]
			if want := name == "claude" && s.Stage; has != want {
				t.Errorf("%s/%s: disable-model-invocation present = %v, want %v", name, s.Name, has, want)
			}
		}
	}
}

func TestRenderFrontmatterValues(t *testing.T) {
	d := agent.Descriptor{Name: "x", SkillFrontmatter: map[string]any{
		"weird key": "a: b", "flag": false, "tags": []any{"x", "y"},
	}}
	got, err := skills.Render(d, skills.Skill{Name: "plait-mr", Stage: true}, "1.0")
	if err != nil {
		t.Fatal(err)
	}
	fm := frontmatter(t, got)
	if fm[skills.KeyVersion] != "1.0" || fm["weird key"] != "a: b" || fm["flag"] != false {
		t.Errorf("frontmatter = %v", fm)
	}
}

func TestRenderReservedKey(t *testing.T) {
	d := agent.Descriptor{Name: "x", SkillFrontmatter: map[string]any{skills.KeyVersion: "1"}}
	_, err := skills.Render(d, skills.Skill{Name: "plait-review", Stage: true}, version)
	e, ok := errors.AsType[*output.Error](err)
	if !ok || e.Code != "config_invalid" || !strings.Contains(e.Message, "agents.x") || e.Fix == "" {
		t.Fatalf("err = %v", err)
	}
}

func TestGuide(t *testing.T) {
	for _, st := range skills.Stages() {
		g, err := skills.Guide(st)
		if err != nil {
			t.Fatalf("%s: %v", st, err)
		}
		if !strings.HasPrefix(g, "# Guide: "+st+" ") {
			t.Errorf("%s: guide starts with %q", st, strings.SplitN(g, "\n", 2)[0])
		}
	}
}

func TestGuideUnknown(t *testing.T) {
	_, err := skills.Guide("deploy")
	e, ok := errors.AsType[*output.Error](err)
	if !ok || e.Code != "unknown_stage" || output.ExitCode(err) != output.ExitInvalid {
		t.Fatalf("err = %v", err)
	}
	for _, st := range skills.Stages() {
		if !strings.Contains(e.Message, st) || !strings.Contains(e.Fix, st) {
			t.Errorf("stage %s missing from %q / %q", st, e.Message, e.Fix)
		}
	}
}

// The reviewer guide restates package.md for the developer; both must name the same answer contract.
func TestReviewerGuideMatchesPackage(t *testing.T) {
	g, err := skills.Guide(skills.StageReviewer)
	if err != nil {
		t.Fatal(err)
	}
	tmpl, err := os.ReadFile(filepath.Join("..", "review", "templates", "package.md.tmpl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{run.FindingsSchema, "`criterion`", "`unchecked_criteria`", "`previous`",
		"`fixed`", "`still_open`", "`not_checked`", "`snapshot`", "`coverage`", "Do not edit files"} {
		if !strings.Contains(g, s) {
			t.Errorf("reviewer guide lacks %q", s)
		}
		if s != run.FindingsSchema && !strings.Contains(string(tmpl), s) {
			t.Errorf("package.md.tmpl lacks %q: update the reviewer guide", s)
		}
	}
}

func frontmatter(t *testing.T, doc []byte) map[string]any {
	t.Helper()
	rest, ok := bytes.CutPrefix(doc, []byte("---\n"))
	if !ok {
		t.Fatalf("no frontmatter:\n%s", doc)
	}
	fm, _, ok := bytes.Cut(rest, []byte("\n---\n"))
	if !ok {
		t.Fatalf("unterminated frontmatter:\n%s", doc)
	}
	var m map[string]any
	if err := yaml.Unmarshal(fm, &m); err != nil {
		t.Fatalf("frontmatter is not YAML: %v\n%s", err, fm)
	}
	return m
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
