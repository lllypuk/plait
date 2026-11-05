package cli

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/lllypuk/plait/internal/agent"
	"github.com/lllypuk/plait/internal/config"
	"github.com/lllypuk/plait/internal/doctor"
	"github.com/lllypuk/plait/internal/install"
	"github.com/lllypuk/plait/internal/output"
)

type setupAgent struct {
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
	Path      string `json:"path,omitempty"`
	Version   string `json:"version,omitempty"`
}

type setupData struct {
	Scope     install.Scope `json:"scope"`
	DryRun    bool          `json:"dry_run"`
	Uninstall bool          `json:"uninstall"`
	Agents    []setupAgent  `json:"agents"`
	Ops       []install.Op  `json:"ops"`
	Changed   bool          `json:"changed"`
	// Checks are the doctor checks of the installed agents; empty for a dry run and an uninstall.
	Checks []doctor.Check `json:"checks"`
}

const columnGap = 2

type setupFlags struct {
	agent     string
	scope     string
	dryRun    bool
	uninstall bool
}

func (a *app) newSetupCmd() *cobra.Command {
	var f setupFlags
	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Install plait skills and deny rules into the coding agents",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			data, err := a.setup(cmd, f)
			if err != nil {
				return err
			}
			text := opsText(data.Ops, data.DryRun, data.Changed, "done; restart the agent sessions to load the skills")
			if len(data.Checks) > 0 {
				text += "\n\n" + doctor.Text(data.Checks)
			}
			if conflicts := opsWith(data.Ops, install.ActionConflict); len(conflicts) > 0 {
				e := conflictError(conflicts, "plait setup")
				e.Data, e.Text = data, text
				return e
			}
			return a.printer().Success(data, text)
		},
	}
	cmd.Flags().StringVar(&f.agent, "agent", "",
		"install into this agent only, built-in or from agents.<name> of the config")
	cmd.Flags().StringVar(&f.scope, "scope", string(install.ScopeUser),
		"user: the home directory of the developer; project: the repository")
	cmd.Flags().BoolVar(&f.dryRun, "dry-run", false, "print the planned operations and write nothing")
	cmd.Flags().BoolVar(&f.uninstall, "uninstall", false, "remove the files and rules plait installed")
	return cmd
}

func (a *app) setup(cmd *cobra.Command, f setupFlags) (setupData, error) {
	scope := install.Scope(f.scope)
	if scope != install.ScopeUser && scope != install.ScopeProject {
		return setupData{}, output.Invalid("unknown_scope", fmt.Sprintf("unknown scope %q", f.scope),
			"pass --scope user or --scope project")
	}
	root, err := a.repoRoot()
	if err != nil {
		return setupData{}, err
	}
	cfg, err := config.Load(config.DefaultSources(root))
	if err != nil {
		return setupData{}, err
	}
	reg, err := agent.Load(cfg.Agents)
	if err != nil {
		return setupData{}, err
	}
	agents, err := pickAgents(cmd, reg, f.agent)
	if err != nil {
		return setupData{}, err
	}
	// Without HOME only user-scope paths fail, and Env.Path reports that with a fix.
	home, _ := os.UserHomeDir()
	env := install.Env{Home: home, Root: root}
	data := setupData{
		Scope:     scope,
		DryRun:    f.dryRun,
		Uninstall: f.uninstall,
		Ops:       []install.Op{},
		Checks:    []doctor.Check{},
	}
	for _, sa := range agents {
		d, _ := reg.Get(sa.Name)
		ops, planErr := install.Plan(d, scope, env, a.opts.Version, f.uninstall)
		if planErr != nil {
			return setupData{}, planErr
		}
		data.Ops = append(data.Ops, ops...)
	}
	data.Agents = agents
	if f.dryRun {
		return data, nil
	}
	if err = install.Apply(data.Ops); err != nil {
		return setupData{}, err
	}
	for _, op := range data.Ops {
		data.Changed = data.Changed || op.Changes()
	}
	if f.uninstall {
		return data, nil
	}
	names := make([]string, len(agents))
	for i, sa := range agents {
		names[i] = sa.Name
	}
	if data.Checks, err = a.doctor(cmd, names, false); err != nil {
		return setupData{}, err
	}
	return data, nil
}

// pickAgents returns the named agent even when it is not in PATH, otherwise every agent found in PATH.
func pickAgents(cmd *cobra.Command, reg *agent.Registry, name string) ([]setupAgent, error) {
	all := reg.All()
	if name != "" {
		d, ok := reg.Get(name)
		if !ok {
			return nil, output.Invalid("unknown_agent", fmt.Sprintf("unknown agent %q; known: %s", name,
				strings.Join(reg.Names(), ", ")),
				"pass one of the known agents to --agent, or describe it in agents."+name+" of .plait/config.yaml")
		}
		all = []agent.Descriptor{d}
	}
	var found []setupAgent
	for _, d := range all {
		det, err := d.Detect(cmd.Context())
		if err != nil {
			return nil, err
		}
		if det.Installed || name != "" {
			found = append(found,
				setupAgent{Name: d.Name, Installed: det.Installed, Path: det.Path, Version: det.Version})
		}
	}
	if len(found) == 0 {
		return nil, output.Invalid("no_agents",
			"no coding agent found in PATH; looked for: "+strings.Join(reg.Names(), ", "),
			"install a coding agent, or pass --agent NAME; if the agent runs under another command, "+
				"set agents.<name>.cmd in .plait/config.yaml, then rerun plait setup")
	}
	return found, nil
}

func opsWith(ops []install.Op, action install.Action) []install.Op {
	var out []install.Op
	for _, op := range ops {
		if op.Action == action {
			out = append(out, op)
		}
	}
	return out
}

func conflictError(conflicts []install.Op, rerun string) *output.Error {
	paths := make([]string, len(conflicts))
	for i, op := range conflicts {
		paths[i] = op.Path
	}
	verb := "file is not plait's and was"
	if len(paths) > 1 {
		verb = "files are not plait's and were"
	}
	return output.NeedsHuman("install_conflict",
		fmt.Sprintf("%d %s left in place: %s", len(paths), verb, strings.Join(paths, ", ")),
		"ask the developer whether to move these files away; plait does not overwrite them. "+
			"The other operations are done; rerun "+rerun+" after the files are moved")
}

// opsText prints one operation per line and then the outcome, done when something changed.
func opsText(ops []install.Op, dryRun, changed bool, done string) string {
	var b strings.Builder
	tw := tabwriter.NewWriter(&b, 0, 0, columnGap, ' ', 0)
	for _, op := range ops {
		line := strings.Join([]string{string(op.Action), op.Agent, op.Item, op.Path}, "\t")
		if op.Detail != "" {
			line += "\t" + op.Detail
		}
		_, _ = fmt.Fprintln(tw, line)
	}
	_ = tw.Flush()
	switch {
	case dryRun:
		b.WriteString("dry run: nothing written")
	case changed:
		b.WriteString(done)
	default:
		b.WriteString("nothing changed")
	}
	return b.String()
}
