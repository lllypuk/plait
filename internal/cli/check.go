package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/lllypuk/plait/internal/check"
	"github.com/lllypuk/plait/internal/output"
	"github.com/lllypuk/plait/internal/run"
	"github.com/lllypuk/plait/internal/vcs"
)

const (
	maxCheckOutput = 64 << 20
	checkSaveFix   = "plait check save <name> --exit-code $? < output.log"
)

var errCheckOutputTooLarge = output.Invalid(
	"check_output_too_large", fmt.Sprintf("the check output is over %d bytes", maxCheckOutput),
	"save only the end of the output: tail -n 1000 output.log | "+checkSaveFix)

func (a *app) newCheckCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Save results of checks the agent ran",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(a.newCheckSaveCmd())
	return cmd
}

func (a *app) newCheckSaveCmd() *cobra.Command {
	var (
		file     string
		exitCode int
	)
	cmd := &cobra.Command{
		Use:   "save <name>",
		Short: "Save a check's exit code and output from stdin or --file, bound to the current working tree",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !cmd.Flags().Changed("exit-code") {
				return output.Invalid("no_exit_code", "the exit code of the check is not given",
					"pass it right after running the check: "+checkSaveFix)
			}
			out, err := a.readCheckOutput(file)
			if err != nil {
				return err
			}
			root, err := a.repoRoot()
			if err != nil {
				return err
			}
			res, err := check.Save(cmd.Context(), run.Open(root), vcs.Repo{Root: root},
				check.Input{Name: args[0], ExitCode: exitCode, Output: out})
			if err != nil {
				return err
			}
			res.Path = relTo(root, res.Path)
			return a.printer().Success(res, checkSaveText(res))
		},
	}
	cmd.Flags().IntVar(&exitCode, "exit-code", 0, "exit code of the check")
	cmd.Flags().StringVar(&file, "file", "", "read the check output from this file instead of stdin")
	return cmd
}

func (a *app) readCheckOutput(file string) ([]byte, error) {
	if file == "" {
		if a.opts.Stdin == nil || isTTY(a.opts.Stdin) {
			return nil, output.Invalid("no_check_input", "no check output on stdin",
				"pipe it: "+checkSaveFix+", or pass --file output.log")
		}
		return readLimited(a.opts.Stdin, maxCheckOutput, errCheckOutputTooLarge)
	}
	if !filepath.IsAbs(file) && a.opts.Dir != "" {
		file = filepath.Join(a.opts.Dir, file)
	}
	f, err := os.Open(filepath.Clean(file))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, output.Invalid("check_file_not_found", fmt.Sprintf("check output file %s does not exist", file),
			"write the check output to a file and pass it with --file, or pipe it: "+checkSaveFix)
	}
	if err != nil {
		return nil, output.Internal(fmt.Errorf("open %s: %w", file, err))
	}
	defer func() { _ = f.Close() }()
	return readLimited(f, maxCheckOutput, errCheckOutputTooLarge)
}

func checkSaveText(res check.Result) string {
	verb := "saved"
	if !res.Changed {
		verb = verbUnchanged
	}
	return fmt.Sprintf("%s check %s (exit %d) for tree %.12s: %s\nnext: plait next",
		verb, res.Name, res.ExitCode, res.Tree, res.Path)
}
