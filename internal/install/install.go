// Package install plans and applies the files plait owns in agent and repository directories.
package install

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/lllypuk/plait/internal/agent"
	"github.com/lllypuk/plait/internal/output"
	"github.com/lllypuk/plait/internal/run"
	"github.com/lllypuk/plait/internal/skills"
)

// Action is what an operation does to its file.
type Action string

// Actions of a plan.
const (
	ActionCreate   Action = "create"
	ActionUpdate   Action = "update"
	ActionSkip     Action = "skip"
	ActionConflict Action = "conflict"
	ActionRemove   Action = "remove"
	ActionWarn     Action = "warn"
)

// Scope selects where agent files go.
type Scope string

// Install scopes.
const (
	ScopeUser    Scope = "user"
	ScopeProject Scope = "project"
)

// ItemPermissions is the Item of deny rule operations.
const ItemPermissions = "permissions"

const (
	dirMode  fs.FileMode = 0o755
	fileMode fs.FileMode = 0o644
)

// Op is one planned change of a file.
type Op struct {
	Action  Action `json:"action"`
	Path    string `json:"path,omitempty"`
	Agent   string `json:"agent,omitempty"`
	Item    string `json:"item"`
	Detail  string `json:"detail,omitempty"`
	content []byte
	mode    fs.FileMode
}

// Changes reports whether Apply touches the file of o.
func (o Op) Changes() bool {
	return o.Action == ActionCreate || o.Action == ActionUpdate || o.Action == ActionRemove
}

// Env locates the scopes: Home for ~/ paths, Root for paths from the repository root.
type Env struct {
	Home string
	Root string
}

// Path resolves a descriptor path of agent name in scope.
func (e Env) Path(name string, scope Scope, p string) (string, error) {
	switch scope {
	case ScopeUser:
		if rest, ok := strings.CutPrefix(p, "~/"); ok && e.Home != "" {
			return filepath.Join(e.Home, filepath.FromSlash(rest)), nil
		}
		if filepath.IsAbs(p) {
			return p, nil
		}
		if e.Home == "" {
			return "", output.Internal(errors.New("home directory is unknown; set HOME"))
		}
	case ScopeProject:
		if e.Root == "" {
			return "", output.Internal(errors.New("repository root is unknown"))
		}
		if filepath.IsLocal(filepath.FromSlash(p)) {
			return filepath.Join(e.Root, filepath.FromSlash(p)), nil
		}
	}
	return "", output.Invalid("config_invalid",
		fmt.Sprintf("agents.%s: %s path %q is outside its scope", name, scope, p),
		fmt.Sprintf("in agents.%s write user paths with ~/ and project paths relative to the repository root, "+
			"then rerun the command", name))
}

// Plan returns the operations that install the skills and deny rules of d in scope, or remove them with uninstall.
func Plan(d agent.Descriptor, scope Scope, env Env, version string, uninstall bool) ([]Op, error) {
	dir, err := env.Path(d.Name, scope, scopePath(d.SkillDirs, scope))
	if err != nil {
		return nil, err
	}
	ops, err := skillOps(d, dir, version, uninstall)
	if err != nil {
		return nil, err
	}
	pops, err := permissionOps(d, scope, env, uninstall)
	if err != nil {
		return nil, err
	}
	return append(ops, pops...), nil
}

// PlanAll returns Plan of every agent with one operation per file.
func PlanAll(agents []agent.Descriptor, scope Scope, env Env, version string, uninstall bool) ([]Op, error) {
	agents, err := shareDeny(agents, scope, env)
	if err != nil {
		return nil, err
	}
	var ops []Op
	for _, d := range agents {
		aops, planErr := Plan(d, scope, env, version, uninstall)
		if planErr != nil {
			return nil, planErr
		}
		ops = append(ops, aops...)
	}
	return Unique(ops), nil
}

// shareDeny gives the first agent of each deny rule file the rules of all agents writing it: Unique keeps only its plan.
func shareDeny(agents []agent.Descriptor, scope Scope, env Env) ([]agent.Descriptor, error) {
	out := slices.Clone(agents)
	owner := map[string]int{}
	for i, d := range out {
		if d.Permissions.Writer == agent.WriterNone {
			continue
		}
		p := agent.Scopes{User: d.Permissions.User, Project: d.Permissions.Project}
		path, err := env.Path(d.Name, scope, scopePath(p, scope))
		if err != nil {
			return nil, err
		}
		j, ok := owner[path]
		if !ok {
			owner[path] = i
			continue
		}
		first := &out[j]
		if first.Permissions.Writer != d.Permissions.Writer {
			return nil, output.Invalid(
				"config_invalid",
				fmt.Sprintf("agents.%s and agents.%s write deny rules to %s with different writers %q and %q",
					first.Name, d.Name, path, first.Permissions.Writer, d.Permissions.Writer),
				fmt.Sprintf(
					"give agents.%s the writer of agents.%s or its own permissions path, then rerun the command",
					d.Name,
					first.Name,
				),
			)
		}
		deny := slices.Clone(first.Permissions.Deny)
		for _, c := range d.Permissions.Deny {
			if !slices.Contains(deny, c) {
				deny = append(deny, c)
			}
		}
		first.Permissions.Deny = deny
	}
	return out, nil
}

// SkillPlan returns the skill operations Plan would make to install d in scope.
func SkillPlan(d agent.Descriptor, scope Scope, env Env, version string) ([]Op, error) {
	dir, err := env.Path(d.Name, scope, scopePath(d.SkillDirs, scope))
	if err != nil {
		return nil, err
	}
	return skillOps(d, dir, version, false)
}

// PermissionPlan returns the deny rule operations Plan would make to install d in scope.
func PermissionPlan(d agent.Descriptor, scope Scope, env Env) ([]Op, error) {
	return permissionOps(d, scope, env, false)
}

func scopePath(s agent.Scopes, scope Scope) string {
	if scope == ScopeProject {
		return s.Project
	}
	return s.User
}

func skillOps(d agent.Descriptor, dir, version string, uninstall bool) ([]Op, error) {
	var ops []Op
	for _, s := range skills.All() {
		op := Op{Path: filepath.Join(dir, s.Name, skills.File), Agent: d.Name, Item: "skill " + s.Name}
		old, err := os.ReadFile(op.Path)
		exists := err == nil
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, output.Internal(err)
		}
		oldVersion, managed := Stamp(old)
		if uninstall {
			switch {
			case !exists:
				op.Action = ActionSkip
			case !managed:
				op.Action, op.Detail = ActionSkip, "not written by plait, left in place"
			default:
				op.Action = ActionRemove
			}
			ops = append(ops, op)
			continue
		}
		want, err := skills.Render(d, s, version)
		if err != nil {
			return nil, err
		}
		op.content, op.mode = want, fileMode
		switch {
		case !exists:
			op.Action = ActionCreate
		case !managed:
			op.Action = ActionConflict
			op.Detail = "file without " + skills.KeyManaged + ": true is in place of the skill; " +
				"move it away, then rerun the command"
		case bytes.Equal(old, want):
			op.Action = ActionSkip
		case oldVersion != version:
			op.Action, op.Detail = ActionUpdate, fmt.Sprintf("plait-version %s -> %s", oldVersion, version)
		default:
			op.Action = ActionUpdate
		}
		ops = append(ops, op)
	}
	return ops, nil
}

// Stamp reads the plait marks from the frontmatter of a skill file.
func Stamp(b []byte) (string, bool) {
	rest, ok := bytes.CutPrefix(b, []byte("---\n"))
	if !ok {
		return "", false
	}
	front, _, ok := bytes.Cut(rest, []byte("\n---\n"))
	if !ok {
		return "", false
	}
	var m map[string]any
	if yaml.Unmarshal(front, &m) != nil || m[skills.KeyManaged] != true {
		return "", false
	}
	v, _ := m[skills.KeyVersion].(string)
	return v, true
}

func permissionOps(d agent.Descriptor, scope Scope, env Env, uninstall bool) ([]Op, error) {
	w, ok := Writer(d.Permissions.Writer)
	if !ok {
		return nil, output.Internal(fmt.Errorf("agents.%s: no installer for writer %q", d.Name, d.Permissions.Writer))
	}
	var path string
	if d.Permissions.Writer != agent.WriterNone {
		var err error
		p := d.Permissions.User
		if scope == ScopeProject {
			p = d.Permissions.Project
		}
		if path, err = env.Path(d.Name, scope, p); err != nil {
			return nil, err
		}
	}
	plan := w.Plan
	if uninstall {
		plan = w.Remove
	}
	ops, err := plan(path, d.Permissions.Deny)
	if err != nil {
		return nil, err
	}
	for i := range ops {
		ops[i].Agent, ops[i].Item = d.Name, ItemPermissions
	}
	return ops, nil
}

// Unique drops repeated operations on one file: a derived agent shares paths with its base, the first agent owns them.
func Unique(ops []Op) []Op {
	seen := map[string]bool{}
	out := make([]Op, 0, len(ops))
	for _, op := range ops {
		key := op.Item + "\x00" + op.Path
		if op.Path == "" || !seen[key] {
			seen[key] = true
			out = append(out, op)
		}
	}
	return out
}

// Apply performs the changing operations in order; with dry-run the caller skips it.
func Apply(ops []Op) error {
	for _, op := range ops {
		var err error
		switch op.Action {
		case ActionCreate, ActionUpdate:
			err = write(op)
		case ActionRemove:
			err = remove(op.Path)
		case ActionSkip, ActionConflict, ActionWarn:
		}
		if err != nil {
			return output.Internal(err)
		}
	}
	return nil
}

func write(op Op) error {
	path := op.Path
	// The rename in WriteFile would replace a symlink, say into a dotfiles repo, with a plain file.
	if target, err := filepath.EvalSymlinks(path); err == nil {
		path = target
	}
	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		return err
	}
	// WriteFile creates through a 0600 temporary file, so the mode is set after the rename.
	if err := run.WriteFile(path, op.content); err != nil {
		return err
	}
	return os.Chmod(path, op.mode)
}

// remove deletes the file and then its directory if that became empty.
func remove(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Remove(filepath.Dir(path)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		if entries, rerr := os.ReadDir(filepath.Dir(path)); rerr == nil && len(entries) > 0 {
			return nil
		}
		return err
	}
	return nil
}
