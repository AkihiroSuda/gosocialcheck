package lookup

import (
	"encoding/json"

	"github.com/spf13/cobra"

	"github.com/AkihiroSuda/gosocialcheck/pkg/cache"
)

func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:                   "lookup",
		Short:                 "[DEBUG] Lookup the database by h1 sum",
		Args:                  cobra.ExactArgs(1),
		RunE:                  action,
		DisableFlagsInUseLine: true,
	}
	return cmd
}

func action(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	sum := args[0]
	c, err := cache.New()
	if err != nil {
		return err
	}
	if _, err = c.LastUpdated(); err != nil {
		return err
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetEscapeHTML(false)
	res, lookupErr := c.Lookup(ctx, sum)
	// res may contain partial contents even on err
	for _, f := range res {
		if err = enc.Encode(f); err != nil {
			return err
		}
	}
	return lookupErr
}
