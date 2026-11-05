package cli

import (
	"github.com/spf13/cobra"

	"github.com/lllypuk/plait/internal/skills"
)

type guideData struct {
	Stage string `json:"stage"`
	Text  string `json:"text"`
}

func (a *app) newGuideCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "guide <stage>",
		Short: "Print the detailed instruction for a stage",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			text, err := skills.Guide(args[0])
			if err != nil {
				return err
			}
			return a.printer().Success(guideData{Stage: args[0], Text: text}, text)
		},
	}
}
