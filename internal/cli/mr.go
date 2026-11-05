package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lllypuk/plait/internal/audit"
	"github.com/lllypuk/plait/internal/config"
	"github.com/lllypuk/plait/internal/mr"
	"github.com/lllypuk/plait/internal/output"
	"github.com/lllypuk/plait/internal/run"
	"github.com/lllypuk/plait/internal/vcs"
)

const maxSummarySize = 64 << 10

var errSummaryTooLarge = output.Invalid(
	"summary_too_large", fmt.Sprintf("the summary is over %d bytes", maxSummarySize),
	"shorten the summary to a few paragraphs and pass it again: plait mr describe --summary-file summary.md")

func (a *app) newMRCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mr",
		Short: "Describe the merge request from the run artifacts",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(a.newMRDescribeCmd())
	return cmd
}

func (a *app) newMRDescribeCmd() *cobra.Command {
	var (
		summaryFile string
		allowStale  bool
	)
	cmd := &cobra.Command{
		Use:   "describe",
		Short: "Write mr.md with the title and body of the merge request; it creates nothing",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			root, err := a.repoRoot()
			if err != nil {
				return err
			}
			summary, inErr := a.readSummary(summaryFile)
			var res mr.Result
			err = a.audited(cmd, root, func(s *run.Store, e *audit.Entry) error {
				if inErr != nil {
					return inErr
				}
				cfg, cfgErr := config.Load(config.DefaultSources(root))
				if cfgErr != nil {
					return cfgErr
				}
				var descErr error
				res, descErr = mr.Describe(cmd.Context(), s, vcs.Repo{Root: root}, cfg,
					mr.Input{Summary: summary, AllowStale: allowStale, Version: a.opts.Version})
				e.Snapshot = res.Snapshot
				return descErr
			})
			if err != nil {
				return err
			}
			return a.printer().Success(res, describeText(res))
		},
	}
	cmd.Flags().StringVar(&summaryFile, "summary-file", "", "read the change summary from this file, - for stdin")
	cmd.Flags().BoolVar(&allowStale, "allow-stale", false,
		"describe the reviewed snapshot even though the working copy changed after it")
	return cmd
}

func (a *app) readSummary(name string) (string, error) {
	switch name {
	case "":
		return "", nil
	case "-":
		if a.opts.Stdin == nil || isTTY(a.opts.Stdin) {
			return "", output.Invalid("no_summary_input", "no summary on stdin",
				"pipe it: plait mr describe --summary-file - < summary.md, or pass the file: --summary-file summary.md")
		}
		b, err := readLimited(a.opts.Stdin, maxSummarySize, errSummaryTooLarge)
		return string(b), err
	}
	if !filepath.IsAbs(name) && a.opts.Dir != "" {
		name = filepath.Join(a.opts.Dir, name)
	}
	f, err := os.Open(filepath.Clean(name))
	if errors.Is(err, fs.ErrNotExist) {
		return "", output.Invalid("summary_not_found", fmt.Sprintf("summary file %s does not exist", name),
			"write the summary to a file and pass its path: plait mr describe --summary-file summary.md")
	}
	if err != nil {
		return "", output.Internal(fmt.Errorf("open %s: %w", name, err))
	}
	defer func() { _ = f.Close() }()
	b, err := readLimited(f, maxSummarySize, errSummaryTooLarge)
	return string(b), err
}

func describeText(res mr.Result) string {
	verb := "described"
	if !res.Changed {
		verb = verbUnchanged
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s\n", verb, res.Path)
	if res.Stale {
		b.WriteString("stale: the working copy changed after the reviewed snapshot, the body says so\n")
	}
	fmt.Fprintf(&b, "\n# %s\n\n%s\nnext: create the merge request with this title and body; plait does not create it",
		res.Title, res.Body)
	return b.String()
}
