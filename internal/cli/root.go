// Package cli wires cobra commands to internal packages.
package cli

import (
	"errors"
	"io"
	"slices"

	"github.com/spf13/cobra"

	"github.com/lllypuk/plait/internal/output"
)

const devVersion = "dev"

// Options are the process inputs of one plait invocation.
type Options struct {
	Version    string
	Args       []string
	Stdout     io.Writer
	Stderr     io.Writer
	IsTerminal func(io.Writer) bool
}

type app struct {
	opts    Options
	json    bool
	started bool
}

// Run executes plait and returns the exit code; it is the single exit point of the CLI.
func Run(opts Options) int {
	if opts.Version == "" {
		opts.Version = devVersion
	}
	if opts.IsTerminal == nil {
		opts.IsTerminal = output.IsTerminal
	}
	a := &app{opts: opts}
	root := a.newRoot()
	root.SetArgs(opts.Args)
	root.SetOut(opts.Stdout)
	root.SetErr(opts.Stderr)

	cmd, err := root.ExecuteC()
	if err == nil {
		return output.ExitOK
	}
	if _, ok := errors.AsType[*output.Error](err); !ok && !a.started {
		err = output.Invalid("usage", err.Error(), cmd.CommandPath()+" --help")
	}
	return a.printer().Fail(err)
}

func (a *app) newRoot() *cobra.Command {
	root := &cobra.Command{
		Use:           "plait",
		Short:         "Guides a coding agent through the team dev-flow",
		SilenceUsage:  true,
		SilenceErrors: true,
		// Errors returned before this hook are usage errors from cobra itself.
		PersistentPreRun: func(*cobra.Command, []string) { a.started = true },
	}
	root.PersistentFlags().BoolVar(&a.json, "json", false, "print the JSON envelope even on a terminal")
	root.AddCommand(a.newVersionCmd())
	return root
}

// printer falls back to scanning raw args because flags are not parsed when cobra fails early.
func (a *app) printer() output.Printer {
	forceJSON := a.json || slices.Contains(argsBeforeDashDash(a.opts.Args), "--json")
	return output.Printer{
		Out:  a.opts.Stdout,
		Err:  a.opts.Stderr,
		Mode: output.SelectMode(forceJSON, a.opts.IsTerminal(a.opts.Stdout)),
	}
}

func argsBeforeDashDash(args []string) []string {
	if i := slices.Index(args, "--"); i >= 0 {
		return args[:i]
	}
	return args
}

type versionData struct {
	Version string `json:"version"`
}

func (a *app) newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the plait version",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return a.printer().Success(versionData{Version: a.opts.Version}, a.opts.Version)
		},
	}
}
