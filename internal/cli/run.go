package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lllypuk/plait/internal/output"
	"github.com/lllypuk/plait/internal/run"
)

type runData struct {
	Run     string   `json:"run"`
	Step    run.Step `json:"step"`
	Dir     string   `json:"dir"`
	Created bool     `json:"created"`
}

type runListData struct {
	Runs []run.Summary `json:"runs"`
}

func (a *app) newRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Create, resume and list runs",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(a.newRunNewCmd(), a.newRunResumeCmd(), a.newRunListCmd())
	return cmd
}

func (a *app) newRunNewCmd() *cobra.Command {
	var key string
	cmd := &cobra.Command{
		Use:   "new <slug>",
		Short: "Start a run and make it current; repeating it the same day returns the same run",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			s, err := a.store()
			if err != nil {
				return err
			}
			st, created, err := s.New(args[0], key)
			if err != nil {
				return err
			}
			verb := "resumed existing"
			if created {
				verb = "created"
			}
			return a.printer().Success(a.runData(s, st, created), fmt.Sprintf("%s run %s", verb, st.ID))
		},
	}
	cmd.Flags().StringVar(&key, "key", "", "task key such as PROJ-123; the run id becomes <KEY>-<slug>")
	return cmd
}

func (a *app) newRunResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resume <id>",
		Short: "Make an existing run current",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			s, err := a.store()
			if err != nil {
				return err
			}
			st, err := s.Resume(args[0])
			if err != nil {
				return err
			}
			return a.printer().Success(a.runData(s, st, false), "current run: "+st.ID)
		},
	}
}

func (a *app) newRunListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List runs; the current one is marked",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			s, err := a.store()
			if err != nil {
				return err
			}
			runs, err := s.List()
			if err != nil {
				return err
			}
			return a.printer().Success(runListData{Runs: runs}, runListText(runs))
		},
	}
}

func runListText(runs []run.Summary) string {
	if len(runs) == 0 {
		return "no runs; start one with: plait run new <slug>"
	}
	var b strings.Builder
	for _, r := range runs {
		mark := "  "
		if r.Current {
			mark = "* "
		}
		fmt.Fprintf(&b, "%s%s  %s\n", mark, r.ID, r.Step)
	}
	return b.String()
}

func (a *app) runData(s *run.Store, st *run.State, created bool) runData {
	dir := s.RunDir(st.ID)
	if root, err := a.repoRoot(); err == nil {
		if rel, relErr := filepath.Rel(root, dir); relErr == nil {
			dir = rel
		}
	}
	return runData{Run: st.ID, Step: st.Step, Dir: dir, Created: created}
}

func (a *app) store() (*run.Store, error) {
	root, err := a.repoRoot()
	if err != nil {
		return nil, err
	}
	return run.Open(root), nil
}

// repoRoot is the nearest ancestor holding .git, or the working directory outside a repository.
func (a *app) repoRoot() (string, error) {
	dir := a.opts.Dir
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", output.Internal(fmt.Errorf("get working directory: %w", err))
		}
		dir = wd
	}
	for d := dir; ; {
		_, err := os.Stat(filepath.Join(d, ".git"))
		if err == nil {
			return d, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", output.Internal(fmt.Errorf("find repository root: %w", err))
		}
		parent := filepath.Dir(d)
		if parent == d {
			return dir, nil
		}
		d = parent
	}
}
