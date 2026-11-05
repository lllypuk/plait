// Package skills renders the agent skills and the stage guides embedded in the binary.
package skills

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"text/template"

	"github.com/lllypuk/plait/internal/agent"
	"github.com/lllypuk/plait/internal/output"
)

// Stages of plait guide <stage>.
const (
	StageCtx      = "ctx"
	StageReview   = "review"
	StageFix      = "fix"
	StageMR       = "mr"
	StageReviewer = "reviewer"
)

// File is the skill file name inside its directory.
const File = "SKILL.md"

var (
	//go:embed templates/*.md.tmpl
	templateFiles embed.FS
	//go:embed guides/*.md
	guideFiles embed.FS

	templates = template.Must(template.New("").Option("missingkey=error"). //nolint:gochecknoglobals // parsed once
			ParseFS(templateFiles, "templates/*.md.tmpl"))

	plainKey = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

// Stages lists the stages that have a guide.
func Stages() []string { return []string{StageCtx, StageReview, StageFix, StageMR, StageReviewer} }

// Skill is one skill directory; Stage skills also get the agent's skill_frontmatter.
type Skill struct {
	Name  string
	Stage bool
}

// All lists the skills plait installs.
func All() []Skill {
	return []Skill{{Name: "plait"}, {Name: "plait-review", Stage: true}, {Name: "plait-mr", Stage: true}}
}

// Frontmatter keys plait sets itself; skill_frontmatter may not override them.
const (
	KeyManaged = "plait-managed"
	KeyVersion = "plait-version"
)

type field struct {
	Key   string
	Value string
}

type data struct {
	Version string
	Extra   []field
}

// Render returns the SKILL.md of s for agent d, stamped with the plait version.
func Render(d agent.Descriptor, s Skill, version string) ([]byte, error) {
	v, err := json.Marshal(version)
	if err != nil {
		return nil, output.Internal(err)
	}
	dt := data{Version: string(v)}
	if s.Stage {
		if dt.Extra, err = extra(d); err != nil {
			return nil, err
		}
	}
	var b bytes.Buffer
	if err = templates.ExecuteTemplate(&b, s.Name+".md.tmpl", dt); err != nil {
		return nil, output.Internal(fmt.Errorf("render skill %s: %w", s.Name, err))
	}
	return b.Bytes(), nil
}

// extra renders skill_frontmatter as YAML lines; JSON values are valid YAML flow scalars.
func extra(d agent.Descriptor) ([]field, error) {
	keys := make([]string, 0, len(d.SkillFrontmatter))
	for k := range d.SkillFrontmatter {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	out := make([]field, 0, len(keys))
	for _, k := range keys {
		if slices.Contains([]string{"name", "description", KeyManaged, KeyVersion}, k) {
			return nil, reservedKey(d.Name, k)
		}
		v, err := json.Marshal(d.SkillFrontmatter[k])
		if err != nil {
			return nil, output.Internal(fmt.Errorf("agents.%s.skill_frontmatter.%s: %w", d.Name, k, err))
		}
		key := k
		if !plainKey.MatchString(k) {
			q, _ := json.Marshal(k)
			key = string(q)
		}
		out = append(out, field{Key: key, Value: string(v)})
	}
	return out, nil
}

func reservedKey(name, key string) error {
	return output.Invalid("config_invalid",
		fmt.Sprintf("agents.%s.skill_frontmatter: key %q is set by plait", name, key),
		fmt.Sprintf("drop %s from agents.%s.skill_frontmatter in the plait config, then rerun the command", key, name))
}

// Guide returns the Markdown guide of a stage.
func Guide(stage string) (string, error) {
	if slices.Contains(Stages(), stage) {
		b, err := fs.ReadFile(guideFiles, "guides/"+stage+".md")
		if err != nil {
			return "", output.Internal(err)
		}
		return string(b), nil
	}
	return "", output.Invalid("unknown_stage", fmt.Sprintf("unknown stage %q, known: %s", stage,
		strings.Join(Stages(), ", ")), "call plait guide <stage> with one of: "+strings.Join(Stages(), ", "))
}
