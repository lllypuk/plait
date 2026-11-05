package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lllypuk/plait/internal/config"
	"github.com/lllypuk/plait/internal/output"
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
	cmd.AddCommand(a.newReviewExportCmd(), a.newReviewImportCmd(), a.newReviewReportCmd())
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
			res, err := review.Export(cmd.Context(), run.Open(root), vcs.Repo{Root: root}, cfg)
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
		verb = verbUnchanged
	}
	var gaps strings.Builder
	for _, g := range res.Gaps {
		fmt.Fprintf(&gaps, "coverage gap %s: %s\n", g.Code, strings.Join(g.Items, ", "))
	}
	return fmt.Sprintf("%s snapshot %d (%.12s), coverage %s\n%spackage: %s\n"+
		"next: open a new agent session, give it the package, save its JSON answer, then plait review import <file>",
		verb, res.Snapshot, res.Commit, res.Coverage, gaps.String(), res.Package)
}

const maxAnswerSize = 4 << 20

var errAnswerTooLarge = output.Invalid(
	"answer_too_large", fmt.Sprintf("the answer is over %d bytes", maxAnswerSize),
	"keep only the JSON object of review-findings/v1 and import it again")

func (a *app) newReviewImportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "import <file|->",
		Short: "Validate the fresh session's JSON answer and bind it to the last snapshot",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			root, err := a.repoRoot()
			if err != nil {
				return err
			}
			data, err := a.readAnswer(args[0])
			if err != nil {
				return err
			}
			res, err := review.Import(cmd.Context(), run.Open(root), vcs.Repo{Root: root}, data, args[0])
			if err != nil {
				return err
			}
			return a.printer().Success(res, importText(res))
		},
	}
}

func (a *app) readAnswer(name string) ([]byte, error) {
	if name == "-" {
		noInput := output.Invalid("no_answer_input", "no answer JSON on stdin",
			"pipe it: plait review import - < answer.json, or pass the file: plait review import answer.json")
		if a.opts.Stdin == nil || isTTY(a.opts.Stdin) {
			return nil, noInput
		}
		b, err := readLimited(a.opts.Stdin, maxAnswerSize, errAnswerTooLarge)
		if err == nil && len(bytes.TrimSpace(b)) == 0 {
			return nil, noInput
		}
		return b, err
	}
	if !filepath.IsAbs(name) && a.opts.Dir != "" {
		name = filepath.Join(a.opts.Dir, name)
	}
	f, err := os.Open(filepath.Clean(name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, output.Invalid("answer_not_found", fmt.Sprintf("answer file %s does not exist", name),
			"save the fresh session's JSON answer to a file and pass its path: plait review import <file>")
	}
	if err != nil {
		return nil, output.Internal(fmt.Errorf("open %s: %w", name, err))
	}
	defer func() { _ = f.Close() }()
	return readLimited(f, maxAnswerSize, errAnswerTooLarge)
}

func importText(res review.ImportResult) string {
	verb := "imported"
	if !res.Changed {
		verb = verbUnchanged
	}
	text := fmt.Sprintf("%s %s: %d findings for snapshot %d (%.12s), coverage %s\n",
		verb, res.Review, res.Findings, res.Snapshot, res.Commit, res.Coverage)
	if res.Stale {
		text += "stale: the working copy changed after the snapshot, the report will say so\n"
	}
	return text + "next: plait review report"
}

func (a *app) newReviewReportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "report",
		Short: "Show the imported review with its coverage label and findings by severity",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := a.repoRoot()
			if err != nil {
				return err
			}
			res, err := review.BuildReport(cmd.Context(), run.Open(root), vcs.Repo{Root: root})
			if err != nil {
				return err
			}
			md, err := review.RenderReport(res)
			if err != nil {
				return err
			}
			return a.printer().Success(res, string(md))
		},
	}
}
