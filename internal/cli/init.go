package cli

import (
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/lllypuk/plait/internal/agent"
	"github.com/lllypuk/plait/internal/config"
	"github.com/lllypuk/plait/internal/install"
	"github.com/lllypuk/plait/internal/output"
)

type initData struct {
	Root   string   `json:"root"`
	DryRun bool     `json:"dry_run"`
	Agents []string `json:"agents"`
	// Ops paths are relative to Root: they are what the team lead commits.
	Ops     []install.Op `json:"ops"`
	Changed bool         `json:"changed"`
}

func (a *app) newInitCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Connect the repository to plait: team config, project skills, AGENTS.md section",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			data, err := a.init(dryRun)
			if err != nil {
				return err
			}
			text := opsText(data.Ops, data.DryRun, data.Changed, "done; review the changes and commit them")
			if conflicts := opsWith(data.Ops, install.ActionConflict); len(conflicts) > 0 {
				e := conflictError(conflicts, "plait init")
				e.Data, e.Text = data, text
				return e
			}
			return a.printer().Success(data, text)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the planned operations and write nothing")
	return cmd
}

func (a *app) init(dryRun bool) (initData, error) {
	root, ok, err := a.gitRoot()
	if err != nil {
		return initData{}, err
	}
	if !ok {
		return initData{}, output.Invalid(
			"not_a_repository",
			"plait init works in a git repository; "+root+" is not in one",
			"run plait init from the repository root, or run git init first",
		)
	}
	cfg, err := config.Load(config.DefaultSources(root))
	if err != nil {
		return initData{}, err
	}
	reg, err := agent.Load(cfg.Agents)
	if err != nil {
		return initData{}, err
	}
	ops, err := install.RepoPlan(reg.All(), install.Env{Root: root}, a.opts.Version)
	if err != nil {
		return initData{}, err
	}
	if !dryRun {
		if err = install.Apply(ops); err != nil {
			return initData{}, err
		}
	}
	data := initData{Root: root, DryRun: dryRun, Agents: reg.Names(), Ops: ops}
	for i, op := range data.Ops {
		data.Changed = data.Changed || !dryRun && op.Changes()
		if rel, relErr := filepath.Rel(root, op.Path); op.Path != "" && relErr == nil {
			data.Ops[i].Path = rel
		}
	}
	return data, nil
}
