package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lllypuk/plait/internal/run"
)

func (a *app) newNextCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "next",
		Short: "Show what to do next in the current run and which command records the result",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			s, err := a.store()
			if err != nil {
				return err
			}
			st, err := s.Current()
			if err != nil {
				return err
			}
			in, err := run.Next(st)
			if err != nil {
				return err
			}
			return a.printer().Success(in, nextText(in))
		},
	}
}

func nextText(in run.Instruction) string {
	var b strings.Builder
	fmt.Fprintf(&b, "run %s, step %s\n%s\n", in.Run, in.Step, in.Do)
	for _, p := range in.Inputs {
		fmt.Fprintf(&b, "input: %s\n", p)
	}
	fmt.Fprintf(&b, "then: %s", in.Output.Cmd)
	if in.Output.Schema != "" {
		fmt.Fprintf(&b, " (schema %s)", in.Output.Schema)
	}
	fmt.Fprintf(&b, "\nguide: %s", in.Guide)
	return b.String()
}
