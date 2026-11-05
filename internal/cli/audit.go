package cli

import (
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lllypuk/plait/internal/audit"
	"github.com/lllypuk/plait/internal/output"
	"github.com/lllypuk/plait/internal/run"
)

// audited runs fn under the store lock and appends its line to audit.jsonl of the current run, refusals included;
// without a current run there is nowhere to write and nothing is logged.
func (a *app) audited(cmd *cobra.Command, root string, fn func(s *run.Store, e *audit.Entry) error) error {
	s, unlock, err := run.Open(root).Hold()
	if err != nil {
		return err
	}
	defer unlock()
	e := audit.Entry{
		Version: a.opts.Version,
		Cmd:     strings.TrimPrefix(cmd.CommandPath(), cmd.Root().Name()+" "),
		Agent:   audit.Agent(os.Getenv),
	}
	if st, curErr := s.Current(); curErr == nil {
		e.Run, e.StepFrom = st.ID, string(st.Step)
	}
	err = fn(s, &e)
	if e.Run == "" {
		return err
	}
	if st, curErr := s.Load(e.Run); curErr == nil {
		e.StepTo = string(st.Step)
	}
	e.TS = s.Now().UTC()
	if err != nil {
		e.Error = output.AsError(err).Code
	}
	if appendErr := audit.Append(s.RunDir(e.Run), e); appendErr != nil && err == nil {
		return output.Internal(appendErr)
	}
	return err
}
