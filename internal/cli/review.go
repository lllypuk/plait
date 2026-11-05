package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/lllypuk/plait/internal/config"
	"github.com/lllypuk/plait/internal/review"
	"github.com/lllypuk/plait/internal/run"
	"github.com/lllypuk/plait/internal/vcs"
)

func (a *app) newReviewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Export the review package, import the answer and report it",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(a.newReviewExportCmd())
	return cmd
}

func (a *app) newReviewExportCmd() *cobra.Command {
	var base string
	cmd := &cobra.Command{
		Use:   "export",
		Short: "Snapshot the working copy and write package.md for a fresh review session",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			flags := map[string]any{}
			if cmd.Flags().Changed("base") {
				flags["review.base"] = base
			}
			root, err := a.repoRoot()
			if err != nil {
				return err
			}
			src := config.DefaultSources(root)
			src.Flags = flags
			cfg, err := config.Load(src)
			if err != nil {
				return err
			}
			res, err := review.Export(cmd.Context(), run.Open(root), vcs.Repo{Root: root}, cfg.Review)
			if err != nil {
				return err
			}
			return a.printer().Success(res, exportText(res))
		},
	}
	cmd.Flags().StringVar(&base, "base", "", "branch or commit the diff starts from (merge-base with HEAD)")
	return cmd
}

func exportText(res review.ExportResult) string {
	verb := "exported"
	if !res.Changed {
		verb = "unchanged"
	}
	return fmt.Sprintf("%s snapshot %d (%.12s), coverage %s\npackage: %s\n"+
		"next: open a new agent session, give it the package, save its JSON answer, then plait review import <file>",
		verb, res.Snapshot, res.Commit, res.Coverage, res.Package)
}
