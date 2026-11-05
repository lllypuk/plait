package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lllypuk/plait/internal/config"
	"github.com/lllypuk/plait/internal/doctor"
	"github.com/lllypuk/plait/internal/output"
)

type doctorData struct {
	Checks []doctor.Check `json:"checks"`
}

func (a *app) newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check the agents, the repository, the proxy and the CA without network access",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			checks, err := a.doctor(cmd, nil)
			if err != nil {
				return err
			}
			data, text := doctorData{Checks: checks}, doctor.Text(checks)
			if failed := doctor.Failed(checks); len(failed) > 0 {
				names := make([]string, len(failed))
				for i, c := range failed {
					names[i] = c.Name
				}
				e := output.NeedsHuman("doctor_failed",
					fmt.Sprintf("%d of %d checks failed: %s", len(failed), len(checks), strings.Join(names, ", ")),
					"apply the fix of each failed check, asking the developer where it needs a human, "+
						"then rerun plait doctor")
				e.Data, e.Text = data, text
				return e
			}
			return a.printer().Success(data, text)
		},
	}
}

// doctor runs the checks for the named agents, every agent when names is empty.
func (a *app) doctor(cmd *cobra.Command, names []string) ([]doctor.Check, error) {
	root, inRepo, err := a.gitRoot()
	if err != nil {
		return nil, err
	}
	env := doctor.Env{Version: a.opts.Version, Sources: config.DefaultSources(root), Agents: names}
	if inRepo {
		env.Root = root
	}
	// Without HOME the user-scope checks fail with a fix of their own.
	env.Home, _ = os.UserHomeDir()
	return doctor.Run(cmd.Context(), env), nil
}
