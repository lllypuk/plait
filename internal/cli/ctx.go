package cli

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/lllypuk/plait/internal/output"
	"github.com/lllypuk/plait/internal/task"
)

const maxTaskSize = 1 << 20

func (a *app) newCtxCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ctx",
		Short: "Save the task context of the current run",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(a.newCtxSaveCmd())
	return cmd
}

func (a *app) newCtxSaveCmd() *cobra.Command {
	var file, source string
	cmd := &cobra.Command{
		Use:   "save [KEY]",
		Short: "Normalize the task Markdown from stdin or --file into task.md of the current run",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			in := task.Input{Source: source}
			if len(args) == 1 {
				in.Key = args[0]
			}
			text, err := a.readTask(file)
			if err != nil {
				return err
			}
			in.Text = text
			s, err := a.store()
			if err != nil {
				return err
			}
			res, err := task.Save(s, in)
			if err != nil {
				return err
			}
			if root, rootErr := a.repoRoot(); rootErr == nil {
				if rel, relErr := filepath.Rel(root, res.Path); relErr == nil {
					res.Path = rel
				}
			}
			return a.printer().Success(res, ctxSaveText(res))
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "read the task Markdown from this file instead of stdin")
	cmd.Flags().StringVar(&source, "source", task.DefaultSource,
		"where the task came from: "+strings.Join(task.Sources(), ", "))
	return cmd
}

func (a *app) readTask(file string) ([]byte, error) {
	if file != "" {
		return a.readTaskFile(file)
	}
	if a.opts.Stdin == nil || isTTY(a.opts.Stdin) {
		return nil, output.Invalid("no_task_input", "no task Markdown on stdin",
			"pipe it: plait ctx save < task.md, or pass --file task.md")
	}
	return readLimited(a.opts.Stdin, maxTaskSize, errTaskTooLarge)
}

func (a *app) readTaskFile(file string) ([]byte, error) {
	if !filepath.IsAbs(file) && a.opts.Dir != "" {
		file = filepath.Join(a.opts.Dir, file)
	}
	f, err := os.Open(filepath.Clean(file))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, output.Invalid("task_file_not_found", fmt.Sprintf("task file %s does not exist", file),
			"write the task Markdown to a file and pass it with --file, or pipe it: plait ctx save < task.md")
	}
	if err != nil {
		return nil, output.Internal(fmt.Errorf("open %s: %w", file, err))
	}
	defer func() { _ = f.Close() }()
	return readLimited(f, maxTaskSize, errTaskTooLarge)
}

const verbUnchanged = "unchanged"

var errTaskTooLarge = output.Invalid(
	"task_too_large", fmt.Sprintf("the task is over %d bytes", maxTaskSize),
	"save only the summary, description and acceptance criteria: plait ctx save < task.md")

func readLimited(r io.Reader, limit int, tooLarge *output.Error) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, int64(limit)+1))
	if err != nil {
		return nil, output.Internal(fmt.Errorf("read input: %w", err))
	}
	if len(b) > limit {
		return nil, tooLarge
	}
	return b, nil
}

func isTTY(r io.Reader) bool {
	f, ok := r.(interface{ Fd() uintptr })
	return ok && term.IsTerminal(int(f.Fd()))
}

func ctxSaveText(res task.Result) string {
	verb := "saved"
	if !res.Changed {
		verb = verbUnchanged
	}
	criteria := "acceptance criteria present"
	if !res.HasCriteria {
		criteria = "no acceptance criteria: marked as a gap, the review will check only the diff"
	}
	return fmt.Sprintf("%s %s (%s)\nnext: plait review export", verb, res.Path, criteria)
}
